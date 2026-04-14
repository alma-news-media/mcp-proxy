package main

import (
	"testing"

	"github.com/tbxark/optional-go"
)

func TestNormalizeRoute(t *testing.T) {
	tests := []struct {
		basePath string
		name     string
		want     string
	}{
		{"/", "github", "/github/"},
		{"", "github", "/github/"},
		{"/api", "github", "/api/github/"},
		{"/api/", "github", "/api/github/"},
		{"api", "github", "/api/github/"},
	}

	for _, tt := range tests {
		t.Run(tt.basePath+"_"+tt.name, func(t *testing.T) {
			got := normalizeRoute(tt.basePath, tt.name)
			if got != tt.want {
				t.Errorf("normalizeRoute(%q, %q) = %q, want %q", tt.basePath, tt.name, got, tt.want)
			}
		})
	}
}

func TestBuildMiddlewares(t *testing.T) {
	t.Run("only recover middleware by default", func(t *testing.T) {
		opts := &OptionsV2{}
		mws := buildMiddlewares("test", opts)
		if len(mws) != 1 {
			t.Errorf("got %d middlewares, want 1 (recover only)", len(mws))
		}
	})

	t.Run("adds logger when enabled", func(t *testing.T) {
		opts := &OptionsV2{LogEnabled: optional.NewField(true)}
		mws := buildMiddlewares("test", opts)
		if len(mws) != 2 {
			t.Errorf("got %d middlewares, want 2 (recover + logger)", len(mws))
		}
	})

	t.Run("adds auth when tokens present", func(t *testing.T) {
		opts := &OptionsV2{AuthTokens: []string{"secret"}}
		mws := buildMiddlewares("test", opts)
		if len(mws) != 2 {
			t.Errorf("got %d middlewares, want 2 (recover + auth)", len(mws))
		}
	})

	t.Run("all middlewares", func(t *testing.T) {
		opts := &OptionsV2{
			AuthTokens: []string{"secret"},
			LogEnabled: optional.NewField(true),
		}
		mws := buildMiddlewares("test", opts)
		if len(mws) != 3 {
			t.Errorf("got %d middlewares, want 3 (recover + logger + auth)", len(mws))
		}
	})
}
