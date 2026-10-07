package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TierCfg is one tier of router.json: the magpie routing group it maps to,
// the Claude Code model alias for it, and the criteria the classifier is
// asked to judge by (project-level router.json may override criteria only).
type TierCfg struct {
	Group       string `json:"group"`
	ClaudeAlias string `json:"claude_alias"`
	Criteria    string `json:"criteria"`
}

// Thresholds are the policy's numeric cut-offs, all in [0, 1], plus the
// per-harness and per-agent overrides SP7 adds (§6.1).
type Thresholds struct {
	TierMin             float64             `json:"tier_min"`
	DissatisfiedMin     float64             `json:"dissatisfied_min"`
	ReviewMin           float64             `json:"review_min"`
	ReviewConfidenceMin float64             `json:"review_confidence_min"`
	Overrides           []ThresholdOverride `json:"overrides,omitempty"`
}

// ThresholdOverride replaces the threshold fields it writes, for one
// harness, one agent kind (main/sub/gateway), or both. An entry writing
// both outranks one writing a single field.
type ThresholdOverride struct {
	Harness             string   `json:"harness,omitempty"`
	Agent               string   `json:"agent,omitempty"`
	TierMin             *float64 `json:"tier_min,omitempty"`
	DissatisfiedMin     *float64 `json:"dissatisfied_min,omitempty"`
	ReviewMin           *float64 `json:"review_min,omitempty"`
	ReviewConfidenceMin *float64 `json:"review_confidence_min,omitempty"`
}

// ReviewConfig is router.json's review section: the end-of-turn review's
// mode, its timeout, and how much of an answer it reads (§6.1).
type ReviewConfig struct {
	Mode           string `json:"mode"`
	TimeoutMs      int    `json:"timeout_ms"`
	MaxAnswerChars int    `json:"max_answer_chars"`
}

// ExperimentConfig decides how sessions split between the router and a
// fixed-tier control group (§9).
type ExperimentConfig struct {
	Enabled       bool   `json:"enabled"`
	RouterPercent int    `json:"router_percent"` // 0–100
	ControlTier   Tier   `json:"control_tier"`
	Salt          string `json:"salt"`
}

// Config is router.json (§4.6).
type Config struct {
	Version           int              `json:"version"`
	RouterGroup       string           `json:"router_group"`
	Tiers             map[Tier]TierCfg `json:"tiers"`
	DefaultTier       Tier             `json:"default_tier"`
	Classifier        string           `json:"classifier"`
	ClassifyTimeoutMs int              `json:"classify_timeout_ms"`
	Thresholds        Thresholds       `json:"thresholds"`
	EscalateTurns     int              `json:"escalate_turns"`
	CacheTTLSeconds   int              `json:"cache_ttl_seconds"`
	FixedAgents       map[string]Tier  `json:"fixed_agents"`
	Experiment        ExperimentConfig `json:"experiment"`
	Review            ReviewConfig     `json:"review"`
}

// defaultFixedAgents is §4.6's list, used when router.json omits it.
var defaultFixedAgents = map[string]Tier{
	"Explore":           TierFast,
	"statusline-setup":  TierFast,
	"claude-code-guide": TierFast,
	"Plan":              TierPerformance,
	"explorer":          TierFast,
}

// Load reads globalPath (typically ~/.config/queqiao/router.json), applies
// defaults, and merges the project-level <cwd>/.queqiao/router.json, which
// may override only each tier's criteria — anything else it says is
// ignored; a missing or unparseable project file is no error. Whether the
// tier groups actually exist in the gateway is not checked here: that
// needs the provider store and belongs to startup wiring (§6.2).
func Load(globalPath, cwd string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(globalPath)
	if err != nil {
		return cfg, fmt.Errorf("router config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("router config %s: %v", globalPath, err)
	}
	cfg.defaults()
	if err := cfg.validate(); err != nil {
		return cfg, fmt.Errorf("router config %s: %v", globalPath, err)
	}
	cfg.mergeProjectCriteria(cwd)
	return cfg, nil
}

// defaults fills the fields §4.6 gives defaults to.
func (c *Config) defaults() {
	if c.RouterGroup == "" {
		c.RouterGroup = "queqiao"
	}
	if c.DefaultTier == "" {
		c.DefaultTier = TierBalanced
	}
	if c.Classifier == "" {
		c.Classifier = "local"
	}
	if c.ClassifyTimeoutMs == 0 {
		c.ClassifyTimeoutMs = 1500
	}
	if c.Thresholds.TierMin == 0 {
		c.Thresholds.TierMin = 0.4
	}
	if c.Thresholds.DissatisfiedMin == 0 {
		c.Thresholds.DissatisfiedMin = 0.7
	}
	if c.Thresholds.ReviewMin == 0 {
		c.Thresholds.ReviewMin = 0.7
	}
	if c.Thresholds.ReviewConfidenceMin == 0 {
		c.Thresholds.ReviewConfidenceMin = 0.5
	}
	if c.Review.Mode == "" {
		c.Review.Mode = ReviewOff
	}
	if c.Review.TimeoutMs == 0 {
		c.Review.TimeoutMs = 5000
	}
	if c.Review.MaxAnswerChars == 0 {
		c.Review.MaxAnswerChars = 6000
	}
	if c.EscalateTurns == 0 {
		c.EscalateTurns = 2
	}
	if c.CacheTTLSeconds == 0 {
		c.CacheTTLSeconds = 300
	}
	if c.FixedAgents == nil {
		c.FixedAgents = defaultFixedAgents
	}
}

// validate checks the structure §6.2 requires. Failure degrades the router
// (the gateway keeps serving); the error is what router status reports.
func (c Config) validate() error {
	for _, tier := range []Tier{TierFast, TierBalanced, TierPerformance} {
		tc, ok := c.Tiers[tier]
		if !ok {
			return fmt.Errorf("tiers: %s missing", tier)
		}
		if tc.Group == "" {
			return fmt.Errorf("tiers: %s has no group", tier)
		}
		if tc.ClaudeAlias == "" {
			return fmt.Errorf("tiers: %s has no claude_alias", tier)
		}
	}
	switch c.DefaultTier {
	case TierFast, TierBalanced, TierPerformance:
	default:
		return fmt.Errorf("default_tier: %q is not a tier", c.DefaultTier)
	}
	th := c.Thresholds
	for name, v := range map[string]float64{
		"tier_min": th.TierMin, "dissatisfied_min": th.DissatisfiedMin,
		"review_min": th.ReviewMin, "review_confidence_min": th.ReviewConfidenceMin,
	} {
		if v < 0 || v > 1 {
			return fmt.Errorf("thresholds.%s %v out of [0,1]", name, v)
		}
	}
	for i, o := range th.Overrides {
		for name, v := range map[string]*float64{
			"tier_min": o.TierMin, "dissatisfied_min": o.DissatisfiedMin,
			"review_min": o.ReviewMin, "review_confidence_min": o.ReviewConfidenceMin,
		} {
			if v != nil && (*v < 0 || *v > 1) {
				return fmt.Errorf("thresholds.overrides[%d].%s %v out of [0,1]", i, name, *v)
			}
		}
	}
	switch c.Review.Mode {
	case ReviewOff, ReviewShadow, ReviewAct:
	default:
		return fmt.Errorf("review.mode %q is not one of off, shadow, act", c.Review.Mode)
	}
	if p := c.Experiment.RouterPercent; p < 0 || p > 100 {
		return fmt.Errorf("experiment.router_percent %d out of [0,100]", p)
	}
	return nil
}

// mergeProjectCriteria applies <cwd>/.queqiao/router.json's tier criteria
// over the global config, ignoring everything else the project file says.
func (c *Config) mergeProjectCriteria(cwd string) {
	for tier, criteria := range ProjectCriteria(cwd) {
		if cur, ok := c.Tiers[tier]; ok {
			cur.Criteria = criteria
			c.Tiers[tier] = cur
		}
	}
}

// ProjectCriteria reads <cwd>/.queqiao/router.json's tier criteria, or
// nil when there is nothing to apply (§4.6: criteria are the only key a
// project file may touch).
func ProjectCriteria(cwd string) map[Tier]string {
	if cwd == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(cwd, ".queqiao", "router.json"))
	if err != nil {
		return nil
	}
	var proj struct {
		Tiers map[Tier]TierCfg `json:"tiers"`
	}
	if json.Unmarshal(b, &proj) != nil {
		return nil
	}
	out := map[Tier]string{}
	for tier, tc := range proj.Tiers {
		if tc.Criteria != "" {
			out[tier] = tc.Criteria
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Review modes (§6.1).
const (
	ReviewOff    = "off"
	ReviewShadow = "shadow"
	ReviewAct    = "act"
)

// PolicyConfig projects the config onto what Choose takes, with the
// thresholds this harness and agent kind get (§6.1): the base values, then
// the overrides — single-field entries in order, then entries writing both
// harness and agent, so a specific one outranks a general one.
func (c Config) PolicyConfigFor(harness, agent string) PolicyConfig {
	kind := agent
	if kind != "main" && kind != "gateway" {
		kind = "sub"
	}
	pc := PolicyConfig{
		FixedAgents:     c.FixedAgents,
		DefaultTier:     c.DefaultTier,
		TierMin:         c.Thresholds.TierMin,
		DissatisfiedMin: c.Thresholds.DissatisfiedMin,
		ReviewMin:       c.Thresholds.ReviewMin,
		ReviewConfMin:   c.Thresholds.ReviewConfidenceMin,
		ReviewMode:      c.Review.Mode,
		EscalateTurns:   c.EscalateTurns,
		CacheTTL:        time.Duration(c.CacheTTLSeconds) * time.Second,
	}
	apply := func(o ThresholdOverride) {
		if o.Harness != "" && o.Harness != harness {
			return
		}
		if o.Agent != "" && o.Agent != kind {
			return
		}
		if o.TierMin != nil {
			pc.TierMin = *o.TierMin
		}
		if o.DissatisfiedMin != nil {
			pc.DissatisfiedMin = *o.DissatisfiedMin
		}
		if o.ReviewMin != nil {
			pc.ReviewMin = *o.ReviewMin
		}
		if o.ReviewConfidenceMin != nil {
			pc.ReviewConfMin = *o.ReviewConfidenceMin
		}
	}
	for _, o := range c.Thresholds.Overrides {
		if o.Harness == "" || o.Agent == "" {
			apply(o)
		}
	}
	for _, o := range c.Thresholds.Overrides {
		if o.Harness != "" && o.Agent != "" {
			apply(o)
		}
	}
	return pc
}

// PolicyConfig is PolicyConfigFor with no harness and the main agent — the
// pre-SP7 shape, still what a plain Choose call gets.
func (c Config) PolicyConfig() PolicyConfig { return c.PolicyConfigFor("", "main") }
