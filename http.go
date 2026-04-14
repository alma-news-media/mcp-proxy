package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/sync/errgroup"
)

// httpServerShutdownTimeout is the per-server drain budget for http.Server.Shutdown.
// Daemon mode shuts down two servers sequentially, so allow enough time for each.
const httpServerShutdownTimeout = 15 * time.Second

type MiddlewareFunc func(http.Handler) http.Handler

func chainMiddleware(h http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for _, mw := range middlewares {
		h = mw(h)
	}
	return h
}

func newAuthMiddleware(tokens []string) MiddlewareFunc {
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(tokens) != 0 {
				token := r.Header.Get("Authorization")
				token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
				if token == "" {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				if _, ok := tokenSet[token]; !ok {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func loggerMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("<%s> Request [%s] %s", prefix, r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

func recoverMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("<%s> Recovered from panic: %v", prefix, err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// swappableHandler is an http.Handler that delegates to an atomically swappable
// inner handler. Used by daemon mode to rebuild routing on config merge
// without restarting the TCP listener.
type swappableHandler struct {
	handler atomic.Value
}

func (h *swappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	v := h.handler.Load()
	if v == nil {
		http.NotFoundHandler().ServeHTTP(w, r)
		return
	}
	inner, ok := v.(http.Handler)
	if !ok {
		http.NotFoundHandler().ServeHTTP(w, r)
		return
	}
	inner.ServeHTTP(w, r)
}

func (h *swappableHandler) swap(next http.Handler) {
	h.handler.Store(http.HandlerFunc(next.ServeHTTP))
}

// wireResult holds the output of wireServers: the built mux and closers for
// all upstream MCP clients that were successfully connected.
type wireResult struct {
	handler http.Handler
	closers []func()
}

func buildMiddlewares(name string, opts *OptionsV2) []MiddlewareFunc {
	mws := []MiddlewareFunc{recoverMiddleware(name)}
	if opts.LogEnabled.OrElse(false) {
		mws = append(mws, loggerMiddleware(name))
	}
	if len(opts.AuthTokens) > 0 {
		mws = append(mws, newAuthMiddleware(opts.AuthTokens))
	}
	return mws
}

func normalizeRoute(basePath, name string) string {
	route := path.Join(basePath, name)
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	if !strings.HasSuffix(route, "/") {
		route += "/"
	}
	return route
}

// serverWireJob holds the parameters for connecting one upstream MCP server.
type serverWireJob struct {
	name         string
	mcpClient    *Client
	srv          *Server
	clientConfig *MCPClientConfigV2
	info         mcp.Implementation
	basePath     string
	httpMux      *http.ServeMux
}

// connectAndRegister connects the upstream MCP client and registers it on the
// mux. Returns a closer on success, or an error if PanicIfInvalid is set and
// the connection fails.
func (j *serverWireJob) connectAndRegister(ctx context.Context) (func(), error) {
	log.Printf("<%s> Connecting", j.name)
	if err := j.mcpClient.addToMCPServer(ctx, j.info, j.srv.mcpServer); err != nil {
		log.Printf("<%s> Failed to add client to server: %v", j.name, err)
		if j.clientConfig.Options.PanicIfInvalid.OrElse(false) {
			return nil, err
		}
		return nil, nil
	}
	log.Printf("<%s> Connected", j.name)

	mws := buildMiddlewares(j.name, j.clientConfig.Options)
	route := normalizeRoute(j.basePath, j.name)
	log.Printf("<%s> Handling requests at %s", j.name, route)
	j.httpMux.Handle(route, chainMiddleware(j.srv.handler, mws...))

	return func() {
		log.Printf("<%s> Shutting down", j.name)
		_ = j.mcpClient.Close()
	}, nil
}

// wireServers creates a new ServeMux with routes for every non-disabled server
// in config. It connects upstream MCP clients in parallel and returns the
// handler plus a list of closer functions for cleanup on shutdown or rebuild.
func wireServers(ctx context.Context, config *Config, baseURL *url.URL) (*wireResult, error) {
	httpMux := http.NewServeMux()
	jobs, err := prepareServerJobs(ctx, config, baseURL, httpMux)
	if err != nil {
		return nil, err
	}

	var eg errgroup.Group
	closerCh := make(chan func(), len(jobs))
	for _, job := range jobs {
		eg.Go(func() error {
			closer, cErr := job.connectAndRegister(ctx)
			if cErr != nil {
				return cErr
			}
			if closer != nil {
				closerCh <- closer
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	close(closerCh)
	log.Printf("All clients initialized")

	var closers []func()
	for fn := range closerCh {
		closers = append(closers, fn)
	}
	return &wireResult{handler: httpMux, closers: closers}, nil
}

// awaitShutdown blocks until SIGINT or SIGTERM, then gracefully shuts down
// the given HTTP server (drain connections, run RegisterOnShutdown hooks).
func awaitShutdown(httpServer *http.Server) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	log.Println("Shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), httpServerShutdownTimeout)
	defer cancel()

	err := httpServer.Shutdown(ctx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func prepareServerJobs(ctx context.Context, config *Config, baseURL *url.URL, httpMux *http.ServeMux) ([]*serverWireJob, error) {
	info := mcp.Implementation{Name: config.McpProxy.Name}
	var jobs []*serverWireJob
	for name, clientConfig := range config.McpServers {
		if clientConfig.Options.Disabled {
			log.Printf("<%s> Disabled", name)
			continue
		}
		mcpClient, err := newMCPClient(name, clientConfig)
		if err != nil {
			return nil, err
		}
		srv, err := newMCPServer(name, config.McpProxy, clientConfig)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &serverWireJob{
			name: name, mcpClient: mcpClient, srv: srv,
			clientConfig: clientConfig, info: info,
			basePath: baseURL.Path, httpMux: httpMux,
		})
	}
	return jobs, nil
}

// startHTTPServer runs the proxy in standalone (non-daemon) mode.
// Clients connect asynchronously: the HTTP server starts accepting requests
// immediately while upstream MCP connections are established in parallel.
func startHTTPServer(config *Config) error {
	baseURL, uErr := url.Parse(config.McpProxy.BaseURL)
	if uErr != nil {
		return uErr
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpMux := http.NewServeMux()
	httpServer := &http.Server{Addr: config.McpProxy.Addr, Handler: httpMux}

	jobs, err := prepareServerJobs(ctx, config, baseURL, httpMux)
	if err != nil {
		return err
	}

	tcpLn, err := listenTCPReuseAddr(config.McpProxy.Addr)
	if err != nil {
		return err
	}

	var eg errgroup.Group
	for _, job := range jobs {
		eg.Go(func() error {
			closer, cErr := job.connectAndRegister(ctx)
			if cErr != nil {
				return cErr
			}
			if closer != nil {
				httpServer.RegisterOnShutdown(closer)
			}
			return nil
		})
	}

	go func() {
		if wErr := eg.Wait(); wErr != nil {
			log.Fatalf("Failed to add clients: %v", wErr)
		}
		log.Printf("All clients initialized")
	}()

	go func() {
		log.Printf("Starting %s server", config.McpProxy.Type)
		log.Printf("%s server listening on %s", config.McpProxy.Type, tcpLn.Addr().String())
		if hErr := httpServer.Serve(tcpLn); hErr != nil && !errors.Is(hErr, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", hErr)
		}
	}()

	return awaitShutdown(httpServer)
}
