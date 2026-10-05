package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/codexcat"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
)

// routerCmd is `queqiao router <init|status|check>` (spec §6.6).
func routerCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("queqiao router takes init, status or check")
	}
	switch args[0] {
	case "init":
		return routerInit(args[1:])
	case "status":
		return routerStatus(args[1:])
	case "check":
		return routerCheck(args[1:])
	}
	return fmt.Errorf("queqiao router takes init, status or check, not %q", args[0])
}

// routerJSONPath is ~/.config/queqiao/router.json.
func routerJSONPath() string {
	return filepath.Join(appdir.Config(), "router.json")
}

// criteria are §4.1's defaults, written by init so projects can override
// them in .queqiao/router.json.
var defaultCriteria = map[router.Tier]string{
	router.TierFast:        "Little work: a question, an explanation, reading logs, running a command, a one-line or mechanical change, a read-only code search",
	router.TierBalanced:    "Some work: an ordinary bug fix or a small feature in code already understood, adding tests, a single-file refactor",
	router.TierPerformance: "Much work: a change across several files, a bug whose cause is unknown, concurrency, performance or security issues, an architecture or design decision, a long multi-step plan",
}

// routerInit is `queqiao router init --preset <name> [--groups-only]
// [--force]`: four routing groups, router.json, and (unless --groups-only)
// the harness mappings of §4.5. Every file written is printed first.
func routerInit(args []string) error {
	var presetID string
	groupsOnly, force := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--preset":
			i++
			if i >= len(args) {
				return fmt.Errorf("--preset needs a name")
			}
			presetID = args[i]
		case "--groups-only":
			groupsOnly = true
		case "--force":
			force = true
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if presetID == "" {
		return fmt.Errorf("init needs --preset <frontier|anthropic|cn>")
	}
	preset, ok := router.Presets()[presetID]
	if !ok {
		return fmt.Errorf("no preset %q; presets: frontier, anthropic, cn", presetID)
	}
	resolved, unresolved := preset.Resolve()
	for _, m := range unresolved {
		fmt.Println(amber.Render("!"), "unresolved (no configured provider serves it yet):", m)
	}

	// 1) the four routing groups (§4.4)
	groups := []provider.Group{
		{ID: "qq-fast", Name: "queqiao fast", Members: resolved[router.TierFast], Routing: "order"},
		{ID: "qq-balanced", Name: "queqiao balanced", Members: resolved[router.TierBalanced], Routing: "order"},
		{ID: "qq-perf", Name: "queqiao performance", Members: resolved[router.TierPerformance], Routing: "order"},
		{ID: "queqiao", Name: "queqiao router", Routing: "order", Affinity: provider.AffinityTurn,
			Members: []string{"group/qq-balanced", "group/qq-perf", "group/qq-fast"}},
	}
	for _, g := range groups {
		if len(g.Members) == 0 {
			return fmt.Errorf("group %s has no members (preset %q): add a provider serving one first", g.ID, presetID)
		}
		if err := provider.SaveGroup(g); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "group", g.ID, "→", strings.Join(g.Members, ", "))
	}

	// 2) router.json (§4.6), §4.1's default criteria, classifier local
	p := routerJSONPath()
	if _, err := os.Stat(p); err == nil && !force {
		return fmt.Errorf("%s exists; pass --force to overwrite", p)
	}
	salt := make([]byte, 8)
	_, _ = rand.Read(salt)
	cfg := router.Config{
		Version:     1,
		RouterGroup: "queqiao",
		Tiers: map[router.Tier]router.TierCfg{
			router.TierFast:        {Group: "qq-fast", ClaudeAlias: "haiku", Criteria: defaultCriteria[router.TierFast]},
			router.TierBalanced:    {Group: "qq-balanced", ClaudeAlias: "sonnet", Criteria: defaultCriteria[router.TierBalanced]},
			router.TierPerformance: {Group: "qq-perf", ClaudeAlias: "opus", Criteria: defaultCriteria[router.TierPerformance]},
		},
		DefaultTier:       router.TierBalanced,
		Classifier:        "local",
		ClassifyTimeoutMs: 1500,
		Thresholds:        router.Thresholds{TierMin: 0.4, DissatisfiedMin: 0.7},
		EscalateTurns:     2,
		CacheTTLSeconds:   300,
		FixedAgents: map[string]router.Tier{
			"Explore": router.TierFast, "statusline-setup": router.TierFast,
			"claude-code-guide": router.TierFast, "Plan": router.TierPerformance,
			"explorer": router.TierFast,
		},
		Experiment: router.ExperimentConfig{Enabled: false, RouterPercent: 50, ControlTier: router.TierPerformance, Salt: hex.EncodeToString(salt)},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := edit.WriteAtomic(p, append(b, '\n')); err != nil {
		return err
	}
	fmt.Println(green.Render("✓"), "wrote", p)

	if groupsOnly {
		return nil
	}

	// 3) Codex (§4.5)
	if err := routerInitCodex(); err != nil {
		fmt.Println(amber.Render("!"), "codex:", err)
	}
	// 4) Claude Code (§4.5)
	if err := routerInitClaudeCode(); err != nil {
		fmt.Println(amber.Render("!"), "claude-code:", err)
	}
	// 5) Pi (§4.5)
	if err := routerInitPi(); err != nil {
		fmt.Println(amber.Render("!"), "pi:", err)
	}
	return nil
}

// codexHome is ~/.codex (overridable in tests).
var codexHome = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// routerInitCodex points Codex at the router group and writes the model
// catalog the router group needs to be selectable (§4.5).
func routerInitCodex() error {
	dir := codexHome()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// the catalog: the router group + the three tier groups
	var ms []catalog.Model
	for _, id := range []string{"group/queqiao", "group/qq-fast", "group/qq-balanced", "group/qq-perf"} {
		ms = append(ms, catalog.Model{ID: id, Name: id})
	}
	catPath := filepath.Join(dir, "queqiao-models.json")
	fmt.Println(muted.Render("  write"), catPath)
	if err := edit.WriteAtomic(catPath, codexcat.Catalog(ms)); err != nil {
		return err
	}
	// config.toml: root scalars — replace if present, else prepend
	path := filepath.Join(dir, "config.toml")
	return codexConfigKeys(path, catPath)
}

// codexConfigKeys sets the three root keys of Codex's config.toml,
// preserving everything else (root scalars live before the first table).
func codexConfigKeys(path, catPath string) error {
	b, _ := edit.Read(path)
	lines := strings.Split(string(b), "\n")
	sets := map[string]string{
		"model":              `"group/queqiao"`,
		"model_provider":     `"magpie"`,
		"model_catalog_json": fmt.Sprintf("%q", catPath),
	}
	wrote := map[string]bool{}
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "[") {
			break // root scalars end at the first table
		}
		for key, value := range sets {
			if strings.HasPrefix(trim, key+" =") || strings.HasPrefix(trim, key+"=") {
				lines[i] = key + " = " + value
				wrote[key] = true
			}
		}
	}
	var top []string
	for key, value := range sets {
		if !wrote[key] {
			top = append(top, key+" = "+value)
		}
	}
	if len(top) > 0 {
		lines = append(top, lines...)
	}
	fmt.Println(muted.Render("  edit"), path, "→", "model=group/queqiao, model_provider=magpie")
	fmt.Println(amber.Render("!"), "restart Codex to see the router group")
	return edit.WriteAtomic(path, []byte(strings.Join(lines, "\n")))
}

// routerInitClaudeCode writes §4.5's env mapping into the project's
// .claude/settings.local.json (cwd).
func routerInitClaudeCode() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(cwd, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.local.json")
	fmt.Println(muted.Render("  edit"), path)
	return edit.SetJSON(path,
		edit.KV{Path: "env.ANTHROPIC_MODEL", Value: "group/queqiao"},
		edit.KV{Path: "env.ANTHROPIC_DEFAULT_HAIKU_MODEL", Value: "group/qq-fast"},
		edit.KV{Path: "env.ANTHROPIC_SMALL_FAST_MODEL", Value: "group/qq-fast"},
		edit.KV{Path: "env.ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "group/qq-balanced"},
		edit.KV{Path: "env.ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "group/qq-perf"},
		edit.KV{Path: "env.ANTHROPIC_DEFAULT_FABLE_MODEL", Value: "group/qq-perf"},
	)
}

// piModelsPath is ~/.pi/agent/models.json (overridable in tests).
var piModelsPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent", "models.json")
}

// routerInitPi points Pi's magpie provider's default model at the balanced
// tier (§4.5; the extension switches per turn after that).
func routerInitPi() error {
	path := piModelsPath()
	fmt.Println(muted.Render("  edit"), path)
	return edit.SetJSON(path,
		edit.KV{Path: "magpie.default", Value: "magpie/group/qq-balanced"},
	)
}

// routerStatus is `queqiao router status`: config validity, the tier→group
// map, the latest decisions (from the gateway if it is serving).
func routerStatus(args []string) error {
	_ = args
	if err := router.ConfigError(); err != nil {
		fmt.Println(amber.Render("!"), "router config:", err)
	}
	cfg, err := router.Load(routerJSONPath(), "")
	if err != nil {
		fmt.Println(amber.Render("!"), "invalid:", err)
		return nil
	}
	fmt.Println("config:", green.Render("ok"), muted.Render("("+routerJSONPath()+")"))
	for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
		tc := cfg.Tiers[tier]
		fmt.Printf("  %-11s %s (%s)\n", tier, "group/"+tc.Group, tc.ClaudeAlias)
	}
	// the live gateway's recent decisions, when it is serving
	base := "http://127.0.0.1:3425"
	if a := os.Getenv("MAGPIE_ADDR"); a != "" {
		base = "http://" + a
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(base + "/v1/queqiao/router")
	if err != nil {
		fmt.Println(muted.Render("  gateway not serving; no recent decisions"))
		return nil
	}
	defer res.Body.Close()
	var body struct {
		Decisions []struct {
			Session string      `json:"session"`
			Tier    router.Tier `json:"tier"`
			Reason  string      `json:"reason"`
			Source  string      `json:"source"`
			Arm     string      `json:"arm"`
		} `json:"decisions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if len(body.Decisions) == 0 {
		fmt.Println(muted.Render("  no decisions yet"))
		return nil
	}
	fmt.Println("recent decisions:")
	for _, d := range body.Decisions {
		fmt.Printf("  %s → %s (%s)\n", d.Session, d.Tier, d.Reason)
	}
	return nil
}

// routerCheck is `queqiao router check [--yes]`: §4.6's smoke test. The
// structural half (config valid, groups present and resolvable, window
// sizes per §4.2) runs offline; the live half — 20 tool-carrying requests
// per member over both protocols — is billable and needs --yes.
func routerCheck(args []string) error {
	yes := false
	for _, a := range args {
		if a == "--yes" {
			yes = true
		}
	}
	cfg, err := router.Load(routerJSONPath(), "")
	if err != nil {
		fmt.Println(amber.Render("✗"), "config:", err)
		return fmt.Errorf("router check failed")
	}
	failed := false
	// structural: every tier group exists with members a configured
	// provider serves, in a big enough window (§4.2 #4: fast ≥128k,
	// balanced ≥200k, performance ≥ the other two's max)
	windows := map[router.Tier]int{router.TierFast: 128000, router.TierBalanced: 200000}
	tierMax := map[router.Tier]int{}
	for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
		g, ok := groupByID(cfg.Tiers[tier].Group)
		if !ok || len(g.Members) == 0 {
			fmt.Println(amber.Render("✗"), tier, "group missing or empty:", cfg.Tiers[tier].Group)
			failed = true
			continue
		}
		for _, m := range g.Members {
			base := strings.Split(m, ":")[0]
			if strings.HasPrefix(base, "group/") {
				continue
			}
			// strip the provider prefix; catalog windows are per model id
			if _, bare, ok := strings.Cut(base, "/"); ok {
				base = bare
			}
			if !modelServed(base) {
				fmt.Println(amber.Render("✗"), tier, "member unserved:", m)
				failed = true
			}
			if w := catalog.ContextOf(base); w > 0 {
				if tier != router.TierPerformance && w < windows[tier] {
					fmt.Println(amber.Render("✗"), tier, "window too small:", m, w)
					failed = true
				}
				if w > tierMax[tier] {
					tierMax[tier] = w
				}
			}
		}
		fmt.Println(green.Render("✓"), tier, "group", cfg.Tiers[tier].Group, fmt.Sprintf("(%d members)", len(g.Members)))
	}
	if max(tierMax[router.TierFast], tierMax[router.TierBalanced]) > tierMax[router.TierPerformance] && tierMax[router.TierPerformance] > 0 {
		fmt.Println(amber.Render("✗"), "performance window below the other tiers' max")
		failed = true
	}
	if !yes {
		fmt.Println(muted.Render("  live member checks skipped (pass --yes; they make real, billable requests)"))
	} else if err := routerCheckLive(cfg); err != nil {
		fmt.Println(amber.Render("✗"), err)
		failed = true
	}
	if failed {
		return fmt.Errorf("router check failed")
	}
	fmt.Println(green.Render("✓"), "router check: ok")
	return nil
}

// modelServed reports whether a configured provider serves the model.
func modelServed(base string) bool {
	for _, p := range provider.All() {
		for _, m := range p.Models {
			if m == base {
				return true
			}
		}
	}
	return false
}

// gatewayUp reports whether the gateway is serving on MAGPIE_ADDR (or
// 127.0.0.1:3425).
func gatewayUp() bool {
	addr := "127.0.0.1:3425"
	if a := os.Getenv("MAGPIE_ADDR"); a != "" {
		addr = a
	}
	c := &http.Client{Timeout: 2 * time.Second}
	res, err := c.Get("http://" + addr + "/v1/models")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == 200
}

// routerCheckLive is §4.6's billable half: 20 tool-carrying requests per
// primary and failover member, over Anthropic Messages and OpenAI
// Responses, through the running gateway so the real translation path is
// exercised. A member fails the gate when tool-argument parse fails more
// than once in 20.
func routerCheckLive(cfg router.Config) error {
	if !gatewayUp() {
		return fmt.Errorf("gateway not serving; live check needs `queqiao serve` running")
	}
	fmt.Println(amber.Render("!"), "live check: real requests to configured providers, billed to them")
	for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
		g, ok := groupByID(cfg.Tiers[tier].Group)
		if !ok {
			continue // structural half already reported it
		}
		for _, m := range g.Members {
			if strings.HasPrefix(m, "group/") {
				continue
			}
			for _, proto := range []string{"anthropic", "responses"} {
				if err := routerProbeMember(m, proto); err != nil {
					return fmt.Errorf("%s (%s over %s): %v", tier, m, proto, err)
				}
			}
			fmt.Println(green.Render("✓"), "live:", tier, m)
		}
	}
	return nil
}

// routerProbeMember sends 20 tool-carrying requests for member over proto
// and fails when the tool call comes back unparseable more than once.
func routerProbeMember(member, proto string) error {
	addr := "127.0.0.1:3425"
	if a := os.Getenv("MAGPIE_ADDR"); a != "" {
		addr = a
	}
	var url, body string
	if proto == "anthropic" {
		url = "http://" + addr + "/v1/messages"
		body = fmt.Sprintf(`{"model":%q,"max_tokens":64,"tools":[{"name":"echo","description":"echo","input_schema":{"type":"object","properties":{"text":{"type":"string"}}}}],"tool_choice":{"type":"tool","name":"echo"},"messages":[{"role":"user","content":"call echo with text probe"}]}`, member)
	} else {
		url = "http://" + addr + "/v1/responses"
		body = fmt.Sprintf(`{"model":%q,"tools":[{"type":"function","name":"echo","description":"echo","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"echo"},"input":[{"role":"user","content":"call echo with text probe"}]}`, member)
	}
	c := &http.Client{Timeout: 120 * time.Second}
	bad := 0
	for i := 0; i < 20; i++ {
		res, err := c.Post(url, "application/json", strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("request %d: %v", i, err)
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("request %d: HTTP %d: %s", i, res.StatusCode, strings.TrimSpace(string(b))[:120])
		}
		if !probeToolCallParseable(proto, b) {
			bad++
		}
	}
	if bad > 1 {
		return fmt.Errorf("%d/20 tool calls unparseable (§4.2 gate: ≤1)", bad)
	}
	return nil
}

// probeToolCallParseable reports whether the reply carries a tool call
// whose arguments parse as JSON.
func probeToolCallParseable(proto string, body []byte) bool {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	m, _ := v.(map[string]any)
	switch proto {
	case "anthropic":
		for _, c := range toList(m["content"]) {
			cm, _ := c.(map[string]any)
			if cm["type"] == "tool_use" {
				var args any
				return json.Unmarshal([]byte(asJSON(cm["input"])), &args) == nil
			}
		}
	case "responses":
		for _, c := range toList(m["output"]) {
			cm, _ := c.(map[string]any)
			if cm["type"] == "function_call" {
				s, _ := cm["arguments"].(string)
				var args any
				return json.Unmarshal([]byte(s), &args) == nil
			}
		}
	}
	return false
}

func toList(v any) []any {
	xs, _ := v.([]any)
	return xs
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// groupByID looks a group up by id (provider.FindGroup is by model id,
// not group id).
func groupByID(id string) (provider.Group, bool) {
	for _, g := range provider.Groups() {
		if g.ID == id {
			return g, true
		}
	}
	return provider.Group{}, false
}
