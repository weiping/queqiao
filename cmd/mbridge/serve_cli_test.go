package main

import (
	"encoding/json"
	"fmt"
	"github.com/weiping/magpie-bridge/internal/router"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonDepsFromRouterJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "router.json")
	os.WriteFile(p, []byte(`{"version":1,"router_group":"mbridge","tiers":{"fast":{"group":"mb-fast","claude_alias":"haiku"},"balanced":{"group":"mb-balanced","claude_alias":"sonnet"},"performance":{"group":"mb-perf","claude_alias":"opus"}},"magpie_url":"http://127.0.0.1:4000","listen":"127.0.0.1:4001"}`), 0o644)
	listen, target, deps := mbridgeDeps(p)
	if listen != "127.0.0.1:4001" || target.Host != "127.0.0.1:4000" || deps == nil {
		t.Fatalf("%s %s %v", listen, target, deps)
	}
}

func TestDaemonDepsWithoutRouterJSONPassesThrough(t *testing.T) {
	listen, target, deps := mbridgeDeps(filepath.Join(t.TempDir(), "missing.json"))
	if listen != "127.0.0.1:3426" || target.Host != "127.0.0.1:3425" || deps != nil {
		t.Fatalf("%s %s %v", listen, target, deps)
	}
}

// mbridge started before `mbridge router init` (the install scripts start
// the service first) picks router.json up when it appears, and again when
// it is edited, without a restart; the sessions it holds survive.
func TestServeReloadsRouterJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	r := newReloader(path)
	r.every = 0
	get := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/bridge/router", nil))
		out := map[string]any{}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	turn := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/bridge/turn", strings.NewReader(`{"session":"s","prompt":"hi","harness":"pi"}`)))
		if rec.Code != 200 {
			t.Fatalf("/turn %d: %s", rec.Code, rec.Body.String())
		}
		out := map[string]any{}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if got := get(); got["valid"] != false {
		t.Fatalf("without router.json: %v", got)
	}
	cfg := `{"version":1,"router_group":"mbridge","default_tier":"%s","classifier":"local","magpie_url":"http://127.0.0.1:1",
	  "tiers":{"fast":{"group":"mb-fast","claude_alias":"haiku"},"balanced":{"group":"mb-balanced","claude_alias":"sonnet"},"performance":{"group":"mb-perf","claude_alias":"opus"}}}`
	os.WriteFile(path, []byte(fmt.Sprintf(cfg, "balanced")), 0o644)
	if got := get(); got["valid"] != true {
		t.Fatalf("after router init: %v", got)
	}
	if got := turn(); got["tier"] != "balanced" {
		t.Fatalf("turn %v", got)
	}
	sessions := r.deps.Sessions
	os.WriteFile(path, []byte(fmt.Sprintf(cfg, "performance")+"\n"), 0o644)
	get()
	if r.deps.Sessions != sessions {
		t.Fatal("a reload dropped the sessions mbridge holds")
	}
	if router.ConfigError() != nil {
		t.Fatalf("config error kept after a good reload: %v", router.ConfigError())
	}
}

// serve takes --detach (the Windows task passes it) and nothing else.
func TestServeArgs(t *testing.T) {
	if d, err := serveArgs(nil); err != nil || d {
		t.Fatalf("no args: %v %v", d, err)
	}
	if d, err := serveArgs([]string{"--detach"}); err != nil || !d {
		t.Fatalf("--detach: %v %v", d, err)
	}
	if _, err := serveArgs([]string{"--listen", "x"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
