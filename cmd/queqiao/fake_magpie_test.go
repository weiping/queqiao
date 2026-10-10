package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/weiping/queqiao/internal/magpie"
)

// fakeMag is official magpie as queqiao's CLI sees it: its CLI (groups,
// agents, usage, version) and its gateway's /v1 and /v1/models.
type fakeMag struct {
	mu       sync.Mutex
	srv      *httptest.Server
	calls    [][]string
	groups   map[string][]string
	order    []string
	served   []string       // provider/model ids
	contexts map[string]int // model or group id → context window
	usageCSV string
	version  string
	agentErr string
	agents   map[string]string
	down     bool
}

// withFakeMagpie gives the test a fresh home and config dir and a fake
// magpie every magpie.Client the CLI makes talks to.
func withFakeMagpie(t *testing.T, served ...string) *fakeMag {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("QUEQIAO_CONFIG_DIR", filepath.Join(home, ".config", "queqiao"))
	f := &fakeMag{groups: map[string][]string{}, served: served, contexts: map[string]int{}, agents: map[string]string{}, version: "magpie v0.1.1100"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.srv.Close)
	old := newMagpie
	newMagpie = func(string) *magpie.Client {
		c := magpie.New(f.srv.URL)
		if f.down {
			c.BaseURL = "http://127.0.0.1:1"
		}
		c.Bin = "magpie"
		c.Run = f.run
		return c
	}
	t.Cleanup(func() { newMagpie = old })
	return f
}

func (f *fakeMag) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/v1":
		io.WriteString(w, `{"object":"magpie"}`)
	case "/v1/models":
		var data []map[string]any
		for _, id := range f.served {
			data = append(data, map[string]any{"id": id, "context_window": f.contexts[id]})
		}
		for _, g := range f.order {
			data = append(data, map[string]any{"id": "group/" + g, "context_window": f.contexts["group/"+g]})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeMag) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.down {
		return []byte("magpie: no gateway"), errors.New("exit status 1")
	}
	switch {
	case len(args) >= 4 && args[0] == "group" && args[1] == "add":
		id := args[2]
		if _, ok := f.groups[id]; !ok {
			f.order = append(f.order, id)
		}
		f.groups[id] = strings.Split(strings.TrimPrefix(args[3], "models="), ",")
		return []byte("✓ group " + id), nil
	case len(args) == 1 && args[0] == "groups":
		var b strings.Builder
		for _, id := range f.order {
			fmt.Fprintf(&b, "  %s  group/%s  order  %s\n", id, id, strings.Join(f.groups[id], ", "))
		}
		return []byte(b.String()), nil
	case len(args) >= 2 && args[0] == "usage":
		return []byte(f.usageCSV), nil
	case len(args) == 1 && args[0] == "version":
		return []byte(f.version + "\n"), nil
	case len(args) == 2:
		if f.agentErr != "" {
			return []byte(f.agentErr), errors.New("exit status 1")
		}
		f.agents[args[0]] = args[1]
		return []byte("✓ " + args[0] + " model " + args[1]), nil
	}
	return nil, fmt.Errorf("fake magpie: %v", args)
}

func (f *fakeMag) called(args ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := strings.Join(append([]string{"magpie"}, args...), " ")
	for _, c := range f.calls {
		if strings.Join(c, " ") == want {
			return true
		}
	}
	return false
}

// captureStdout runs fn with os.Stdout going to a pipe and returns what it
// printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	err := fn()
	w.Close()
	os.Stdout = old
	return <-done, err
}
