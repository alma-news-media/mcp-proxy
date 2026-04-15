package main

import "testing"

func TestVersionString_nonEmpty(t *testing.T) {
	if versionString() == "" {
		t.Fatal("versionString() must not be empty")
	}
}
