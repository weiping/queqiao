package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validJSON is a complete, §4.6-shaped router.json.
const validJSON = `{
  "version": 1,
  "router_group": "queqiao",
  "tiers": {
    "fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "fast criteria" },
    "balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "balanced criteria" },
    "performance": { "group": "qq-perf",     "claude_alias": "opus",   "criteria": "perf criteria" }
  },
  "default_tier": "balanced",
  "classifier": "local",
  "classify_timeout_ms": 1500,
  "thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7 },
  "escalate_turns": 2,
  "cache_ttl_seconds": 300,
  "fixed_agents": { "Explore": "fast", "Plan": "performance" },
  "experiment": { "enabled": false, "router_percent": 50, "control_tier": "performance", "salt": "s1" }
}`

// writeGlobal writes content to a temp router.json and returns its path.
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "router.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(writeGlobal(t, validJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RouterGroup != "queqiao" || cfg.DefaultTier != TierBalanced || cfg.Classifier != "local" {
		t.Fatalf("cfg: %+v", cfg)
	}
	if cfg.Tiers[TierFast].Group != "qq-fast" || cfg.Tiers[TierFast].ClaudeAlias != "haiku" {
		t.Fatalf("fast tier: %+v", cfg.Tiers[TierFast])
	}
	if cfg.Tiers[TierPerformance].Criteria != "perf criteria" {
		t.Fatalf("perf criteria: %q", cfg.Tiers[TierPerformance].Criteria)
	}
	if cfg.Thresholds.TierMin != 0.4 || cfg.Thresholds.DissatisfiedMin != 0.7 {
		t.Fatalf("thresholds: %+v", cfg.Thresholds)
	}
	if cfg.FixedAgents["Plan"] != TierPerformance {
		t.Fatalf("fixed agents: %+v", cfg.FixedAgents)
	}
	if !cfg.Experiment.Enabled && cfg.Experiment.RouterPercent != 50 {
		t.Fatalf("experiment: %+v", cfg.Experiment)
	}
}

func TestLoadDefaultsZeroFields(t *testing.T) {
	// classifier/timeout/escalate/cache_ttl left out: defaults apply.
	min := `{"tiers": {
		"fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "f" },
		"balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "b" },
		"performance": { "group": "qq-perf",     "claude_alias": "opus",   "criteria": "p" }
	}}`
	cfg, err := Load(writeGlobal(t, min), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultTier != TierBalanced {
		t.Fatalf("default tier %q", cfg.DefaultTier)
	}
	if cfg.Classifier != "local" || cfg.ClassifyTimeoutMs != 1500 {
		t.Fatalf("classifier %q timeout %d", cfg.Classifier, cfg.ClassifyTimeoutMs)
	}
	if cfg.EscalateTurns != 2 || cfg.CacheTTLSeconds != 300 {
		t.Fatalf("escalate %d ttl %d", cfg.EscalateTurns, cfg.CacheTTLSeconds)
	}
	if cfg.Thresholds.TierMin != 0.4 || cfg.Thresholds.DissatisfiedMin != 0.7 {
		t.Fatalf("thresholds %+v", cfg.Thresholds)
	}
	if len(cfg.FixedAgents) == 0 {
		t.Fatal("no default fixed agents")
	}
}

func TestLoadMissingTiers(t *testing.T) {
	if _, err := Load(writeGlobal(t, `{"default_tier": "balanced"}`), ""); err == nil {
		t.Fatal("missing tiers accepted")
	}
}

func TestLoadMissingTierKey(t *testing.T) {
	bad := `{"tiers": {
		"fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "f" },
		"balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "b" }
	}}`
	if _, err := Load(writeGlobal(t, bad), ""); err == nil {
		t.Fatal("two tiers accepted")
	}
}

func TestLoadBadGroup(t *testing.T) {
	bad := `{"tiers": {
		"fast":        { "group": "",             "claude_alias": "haiku",  "criteria": "f" },
		"balanced":    { "group": "qq-balanced",  "claude_alias": "sonnet", "criteria": "b" },
		"performance": { "group": "qq-perf",      "claude_alias": "opus",   "criteria": "p" }
	}}`
	if _, err := Load(writeGlobal(t, bad), ""); err == nil {
		t.Fatal("empty group accepted")
	}
}

func TestLoadThresholdsOutOfRange(t *testing.T) {
	for _, th := range []string{
		`{"tier_min": 1.5, "dissatisfied_min": 0.7}`,
		`{"tier_min": 0.4, "dissatisfied_min": -0.1}`,
	} {
		var tiers map[string]map[string]string
		if err := json.Unmarshal([]byte(`{
			"fast":{"group":"qq-fast","claude_alias":"haiku","criteria":"f"},
			"balanced":{"group":"qq-balanced","claude_alias":"sonnet","criteria":"b"},
			"performance":{"group":"qq-perf","claude_alias":"opus","criteria":"p"}}`), &tiers); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"tiers": tiers, "thresholds": json.RawMessage(th)})
		if _, err := Load(writeGlobal(t, string(body)), ""); err == nil {
			t.Fatalf("thresholds %s accepted", th)
		}
	}
}

func TestLoadRouterPercentOutOfRange(t *testing.T) {
	body := `{"tiers": {
		"fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "f" },
		"balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "b" },
		"performance": { "group": "qq-perf",     "claude_alias": "opus",   "criteria": "p" }
	}, "experiment": { "enabled": true, "router_percent": 150, "control_tier": "performance", "salt": "s" }}`
	if _, err := Load(writeGlobal(t, body), ""); err == nil {
		t.Fatal("router_percent 150 accepted")
	}
}

func TestProjectCriteriaOverride(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".queqiao"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := `{
		"default_tier": "performance",
		"tiers": {
			"fast":        { "criteria": "project fast criteria" },
			"balanced":    { "group": "evil-group", "criteria": "project balanced criteria" }
		}}`
	if err := os.WriteFile(filepath.Join(cwd, ".queqiao", "router.json"), []byte(proj), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(writeGlobal(t, validJSON), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tiers[TierFast].Criteria != "project fast criteria" {
		t.Fatalf("fast criteria %q", cfg.Tiers[TierFast].Criteria)
	}
	if cfg.Tiers[TierBalanced].Criteria != "project balanced criteria" {
		t.Fatalf("balanced criteria %q", cfg.Tiers[TierBalanced].Criteria)
	}
	// everything but criteria is ignored:
	if cfg.DefaultTier != TierBalanced {
		t.Fatalf("project default_tier leaked: %q", cfg.DefaultTier)
	}
	if cfg.Tiers[TierBalanced].Group != "qq-balanced" {
		t.Fatalf("project group leaked: %q", cfg.Tiers[TierBalanced].Group)
	}
}

func TestPolicyConfigConversion(t *testing.T) {
	cfg, err := Load(writeGlobal(t, validJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	pc := cfg.PolicyConfig()
	if pc.DefaultTier != TierBalanced || pc.TierMin != 0.4 || pc.DissatisfiedMin != 0.7 {
		t.Fatalf("pc: %+v", pc)
	}
	if pc.EscalateTurns != 2 || pc.CacheTTL != 300*time.Second {
		t.Fatalf("pc escalate %d ttl %v", pc.EscalateTurns, pc.CacheTTL)
	}
	if pc.FixedAgents["Explore"] != TierFast {
		t.Fatalf("pc fixed agents: %+v", pc.FixedAgents)
	}
}

// ---------------------------------------------------------------- SP7

// A router.json from before SP7 loads unchanged: the new fields take their
// defaults rather than erroring.
func TestOldConfigGetsReviewDefaults(t *testing.T) {
	cfg, err := Load(writeGlobal(t, validJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Thresholds.ReviewMin != 0.7 || cfg.Thresholds.ReviewConfidenceMin != 0.5 {
		t.Fatalf("thresholds: %+v", cfg.Thresholds)
	}
	if cfg.Review.Mode != "off" || cfg.Review.TimeoutMs != 5000 || cfg.Review.MaxAnswerChars != 6000 {
		t.Fatalf("review: %+v", cfg.Review)
	}
}

func TestReviewModeValidated(t *testing.T) {
	bad := strings.Replace(validJSON, `"experiment"`, `"review": { "mode": "maybe" }, "experiment"`, 1)
	if _, err := Load(writeGlobal(t, bad), ""); err == nil || !strings.Contains(err.Error(), "review.mode") {
		t.Fatalf("err: %v", err)
	}
}

// overrides: an entry writing both harness and agent outranks one writing
// a single field; single-field entries apply in order among themselves.
func TestOverrideOrder(t *testing.T) {
	withOverrides := strings.Replace(validJSON,
		`"thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7 }`,
		`"thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7, "overrides": [
			{ "agent": "sub", "tier_min": 0.5 },
			{ "harness": "codex", "agent": "sub", "tier_min": 0.6 },
			{ "harness": "codex", "review_min": 0.65 } ] }`, 1)
	cfg, err := Load(writeGlobal(t, withOverrides), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PolicyConfigFor("codex", "Explore").TierMin; got != 0.6 {
		t.Fatalf("codex/sub tier_min = %v, want 0.6 (both-fields entry wins)", got)
	}
	if got := cfg.PolicyConfigFor("codex", "Explore").ReviewMin; got != 0.65 {
		t.Fatalf("codex/sub review_min = %v, want 0.65", got)
	}
	if got := cfg.PolicyConfigFor("pi", "Explore").TierMin; got != 0.5 {
		t.Fatalf("pi/sub tier_min = %v, want 0.5 (agent-only entry)", got)
	}
	if got := cfg.PolicyConfigFor("pi", "main").TierMin; got != 0.4 {
		t.Fatalf("pi/main tier_min = %v, want 0.4 (no match)", got)
	}
}

func TestOverrideThresholdRange(t *testing.T) {
	bad := strings.Replace(validJSON,
		`"thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7 }`,
		`"thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7, "overrides": [ { "harness": "codex", "review_min": 1.2 } ] }`, 1)
	if _, err := Load(writeGlobal(t, bad), ""); err == nil {
		t.Fatal("out-of-range override accepted")
	}
}

// A router.json from before SP8 has neither listen nor magpie_url: queqiaod
// listens on 3426 and finds magpie on 3425.
func TestOldConfigGetsListenAndMagpieDefaults(t *testing.T) {
	cfg, err := Load(writeGlobal(t, validJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:3426" || cfg.MagpieURL != "http://127.0.0.1:3425" {
		t.Fatalf("listen %q magpie %q", cfg.Listen, cfg.MagpieURL)
	}
}
