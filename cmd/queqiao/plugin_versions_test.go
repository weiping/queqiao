package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMarketplaceListsThePluginsOwnVersion keeps the Claude Code
// marketplace entry on the version the plugin's own manifest carries
// (the entry said 0.1.0 after the plugin moved to 0.1.1).
func TestMarketplaceListsThePluginsOwnVersion(t *testing.T) {
	var market struct {
		Plugins []struct {
			Name    string `json:"name"`
			Source  string `json:"source"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	read := func(path string, v any) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	read("../../.claude-plugin/marketplace.json", &market)
	if len(market.Plugins) == 0 {
		t.Fatal("marketplace lists no plugins")
	}
	for _, p := range market.Plugins {
		var own struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		read(filepath.Join("../..", p.Source, ".claude-plugin/plugin.json"), &own)
		if p.Version != own.Version {
			t.Errorf("marketplace lists %s at %s; %s/.claude-plugin/plugin.json says %s", p.Name, p.Version, p.Source, own.Version)
		}
	}
}
