package router

import (
	"strings"

	"github.com/weiping/magpie-bridge/internal/magpie"
)

// Preset is one §4.3 starter config: each tier's primary and failover
// members. "<p>/" in a member means "whatever provider serves this model".
type Preset struct {
	ID    string
	Tiers map[Tier][2]string // [primary, failover], members in magpie group syntax
}

// presets holds §4.3's three presets verbatim.
var presets = map[string]Preset{
	"frontier": {ID: "frontier", Tiers: map[Tier][2]string{
		TierFast:        {"<p>/glm-5.3-flash:xhigh", "deepseek/deepseek-v4-flash"},
		TierBalanced:    {"<p>/gpt-5.6-sol:medium", "<p>/claude-sonnet-5:medium"},
		TierPerformance: {"<p>/gpt-6-astra:low", "<p>/claude-opus-5-5:high"},
	}},
	"anthropic": {ID: "anthropic", Tiers: map[Tier][2]string{
		TierFast:        {"anthropic/claude-haiku-4-5", "openrouter/anthropic/claude-haiku-4-5"},
		TierBalanced:    {"anthropic/claude-sonnet-5:medium", "openrouter/anthropic/claude-sonnet-5:medium"},
		TierPerformance: {"anthropic/claude-opus-5-5:high", "openrouter/anthropic/claude-opus-5.5:high"},
	}},
	"cn": {ID: "cn", Tiers: map[Tier][2]string{
		TierFast:     {"deepseek/deepseek-v4-flash", "glm/glm-5.3-flash:high"},
		TierBalanced: {"moonshot/kimi-k2.5", "glm/glm-5.3:high"},
		// §4.3: cn's performance tier is frontier's for now; where nothing
		// serves those models, router init gives mb-perf balanced's members
		// and says how to set a domestic model of the user's choosing.
		TierPerformance: {"<p>/gpt-6-astra:low", "<p>/claude-opus-5-5:high"},
	}},
}

// Presets returns them by name.
func Presets() map[string]Preset { return presets }

// Resolve turns "<p>/model[:effort]" members into "provider/model[:effort]"
// using served, the model ids magpie lists (GET /v1/models). A placeholder nothing serves is
// reported unresolved and left out: "<p>" names no provider, so as a group
// member it could never answer. A concrete member stays even while nothing
// serves it yet (its provider can be added later).
func (p Preset) Resolve(served []string) (resolved map[Tier][]string, unresolved []string) {
	resolved = map[Tier][]string{}
	for _, tier := range []Tier{TierFast, TierBalanced, TierPerformance} {
		for _, member := range p.Tiers[tier] {
			if strings.HasPrefix(member, "<p>/") {
				m := member[4:] // model[:effort]
				base, _, _ := strings.Cut(m, ":")
				id := providerFor(served, base)
				if id == "" {
					unresolved = append(unresolved, member)
					continue
				}
				member = id + "/" + m
			}
			resolved[tier] = append(resolved[tier], member)
		}
	}
	return resolved, unresolved
}

// providerFor is the first provider in served ("provider/model" ids)
// serving base; magpie's groups are not providers.
func providerFor(served []string, base string) string {
	for _, id := range served {
		prov, model, ok := strings.Cut(id, "/")
		if !ok || prov+"/" == magpie.GroupPrefix {
			continue
		}
		if model == base || strings.HasPrefix(model, base+"/") {
			return prov
		}
	}
	return ""
}
