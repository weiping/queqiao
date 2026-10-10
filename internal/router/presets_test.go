package router

import (
	"strings"
	"testing"
)

// served is what /v1/models lists for a magpie with DeepSeek and
// Moonshot configured (groups included, as magpie lists them).
var served = []string{"deepseek/deepseek-v4-flash", "moonshot/kimi-k2.5", "group/mb-fast"}

// A "<p>/model" placeholder nothing configured serves is no member at all
// ("<p>" names no provider): it is reported, never written into a group,
// where it would sit as a member that can never answer ("no member ready").
func TestResolveDropsPlaceholdersNothingServes(t *testing.T) {
	resolved, unresolved := Presets()["cn"].Resolve(served)
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
	resolved, _ := Presets()["cn"].Resolve(served)
	if got := resolved[TierFast]; len(got) != 2 || got[0] != "deepseek/deepseek-v4-flash" || got[1] != "glm/glm-5.3-flash:high" {
		t.Fatalf("fast: %v", got)
	}
}

// A placeholder some provider serves resolves to that provider, its
// effort kept.
func TestResolveFillsServedPlaceholders(t *testing.T) {
	resolved, unresolved := Presets()["cn"].Resolve(append([]string{"relay/gpt-6-astra"}, served...))
	if got := resolved[TierPerformance]; len(got) != 1 || got[0] != "relay/gpt-6-astra:low" {
		t.Fatalf("performance: %v", got)
	}
	if len(unresolved) != 1 || unresolved[0] != "<p>/claude-opus-5-5:high" {
		t.Fatalf("unresolved: %v", unresolved)
	}
}

// A group magpie lists is never taken for a provider serving a model.
func TestResolveUsesServedModels(t *testing.T) {
	resolved, unresolved := Presets()["cn"].Resolve([]string{"group/gpt-6-astra", "relay/gpt-6-astra"})
	if got := resolved[TierPerformance]; len(got) != 1 || got[0] != "relay/gpt-6-astra:low" {
		t.Fatalf("performance: %v (unresolved %v)", got, unresolved)
	}
}
