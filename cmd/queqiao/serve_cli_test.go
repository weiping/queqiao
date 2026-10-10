package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueqiaodDepsFromRouterJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "router.json")
	os.WriteFile(p, []byte(`{"version":1,"router_group":"queqiao","tiers":{"fast":{"group":"qq-fast","claude_alias":"haiku"},"balanced":{"group":"qq-balanced","claude_alias":"sonnet"},"performance":{"group":"qq-perf","claude_alias":"opus"}},"magpie_url":"http://127.0.0.1:4000","listen":"127.0.0.1:4001"}`), 0o644)
	listen, target, deps := queqiaodDeps(p)
	if listen != "127.0.0.1:4001" || target.Host != "127.0.0.1:4000" || deps == nil {
		t.Fatalf("%s %s %v", listen, target, deps)
	}
}

func TestQueqiaodDepsWithoutRouterJSONPassesThrough(t *testing.T) {
	listen, target, deps := queqiaodDeps(filepath.Join(t.TempDir(), "missing.json"))
	if listen != "127.0.0.1:3426" || target.Host != "127.0.0.1:3425" || deps != nil {
		t.Fatalf("%s %s %v", listen, target, deps)
	}
}
