package main

import (
	"strings"
	"testing"
)

// `queqiao web` names itself queqiao in what it prints, like the rest of
// the CLI since the rename: an unknown flag's message tells the user the
// command they actually have.
func TestWebSaysQueqiao(t *testing.T) {
	err := webCmd([]string{"--bogus"})
	if err == nil || !strings.HasPrefix(err.Error(), "queqiao web: unknown") || strings.Contains(err.Error(), "magpie web") {
		t.Fatalf("err = %v", err)
	}
}
