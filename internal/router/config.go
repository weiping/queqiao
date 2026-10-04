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

// Thresholds are the policy's numeric cut-offs, both in [0, 1].
type Thresholds struct {
	TierMin         float64 `json:"tier_min"`
	DissatisfiedMin float64 `json:"dissatisfied_min"`
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
	if th := c.Thresholds; th.TierMin < 0 || th.TierMin > 1 || th.DissatisfiedMin < 0 || th.DissatisfiedMin > 1 {
		return fmt.Errorf("thresholds out of [0,1]: %+v", th)
	}
	if p := c.Experiment.RouterPercent; p < 0 || p > 100 {
		return fmt.Errorf("experiment.router_percent %d out of [0,100]", p)
	}
	return nil
}

// mergeProjectCriteria applies <cwd>/.queqiao/router.json's tier criteria
// over the global config, ignoring everything else the project file says.
func (c *Config) mergeProjectCriteria(cwd string) {
	if cwd == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(cwd, ".queqiao", "router.json"))
	if err != nil {
		return // no project override
	}
	var proj struct {
		Tiers map[Tier]TierCfg `json:"tiers"`
	}
	if json.Unmarshal(b, &proj) != nil {
		return // an unparseable project file is ignored
	}
	for tier, tc := range proj.Tiers {
		if tc.Criteria != "" {
			if cur, ok := c.Tiers[tier]; ok {
				cur.Criteria = tc.Criteria
				c.Tiers[tier] = cur
			}
		}
	}
}

// PolicyConfig projects the config onto what Choose takes.
func (c Config) PolicyConfig() PolicyConfig {
	return PolicyConfig{
		FixedAgents:     c.FixedAgents,
		DefaultTier:     c.DefaultTier,
		TierMin:         c.Thresholds.TierMin,
		DissatisfiedMin: c.Thresholds.DissatisfiedMin,
		EscalateTurns:   c.EscalateTurns,
		CacheTTL:        time.Duration(c.CacheTTLSeconds) * time.Second,
	}
}
