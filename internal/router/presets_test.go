package router

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func presetHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, p := range []provider.Provider{
		{ID: "deepseek", Name: "DeepSeek", Key: "k", Models: []string{"deepseek-v4-flash"}, Chat: "http://127.0.0.1:1/v1"},
		{ID: "moonshot", Name: "Moonshot", Key: "k", Models: []string{"kimi-k2.5"}, Chat: "http://127.0.0.1:1/v1"},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
}

// A "<p>/model" placeholder nothing configured serves is no member at all
// ("<p>" names no provider): it is reported, never written into a group,
// where it would sit as a member that can never answer ("no member ready").
func TestResolveDropsPlaceholdersNothingServes(t *testing.T) {
	presetHome(t)
	resolved, unresolved := Presets()["cn"].Resolve()
	for _, m := range resolved[TierPerformance] {
		if strings.HasPrefix(m, "<p>/") {
			t.Fatalf("performance kept the placeholder %q: %v", m, resolved[TierPerformance])
		}
	}
	if len(unresolved) != 2 || unresolved[0] != "<p>/gpt-6-astra:low" || unresolved[1] != "<p>/claude-opus-5-5:high" {
		t.Fatalf("unresolved: %v", unresolved)
	}
}

// A concrete member stays even while nothing serves it yet: adding its
// provider later makes it answer, and the group shows it as not served.
func TestResolveKeepsConcreteMembers(t *testing.T) {
	presetHome(t)
	resolved, _ := Presets()["cn"].Resolve()
	if got := resolved[TierFast]; len(got) != 2 || got[0] != "deepseek/deepseek-v4-flash" || got[1] != "glm/glm-5.3-flash:high" {
		t.Fatalf("fast: %v", got)
	}
}

// A placeholder some provider serves resolves to that provider, its
// effort kept.
func TestResolveFillsServedPlaceholders(t *testing.T) {
	presetHome(t)
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "k", Models: []string{"gpt-6-astra"}, Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	resolved, unresolved := Presets()["cn"].Resolve()
	if got := resolved[TierPerformance]; len(got) != 1 || got[0] != "relay/gpt-6-astra:low" {
		t.Fatalf("performance: %v", got)
	}
	if len(unresolved) != 1 || unresolved[0] != "<p>/claude-opus-5-5:high" {
		t.Fatalf("unresolved: %v", unresolved)
	}
}
