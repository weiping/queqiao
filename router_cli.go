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
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/codexcfg"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/magpie"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
	ledgerUsage "github.com/yetone/magpie/internal/usage"
)

// routerCmd is `queqiao router <init|status|check>` (spec §6.6).
func routerCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("queqiao router takes init, status, check, report or calibrate")
	}
	switch args[0] {
	case "init":
		return routerInit(args[1:])
	case "status":
		return routerStatus(args[1:])
	case "check":
		return routerCheck(args[1:])
	case "report":
		return routerReport(args[1:])
	case "calibrate":
		return routerCalibrate(args[1:])
	case "serve": // fork: magpie owns `serve`; Task 9 makes it `queqiao serve`
		return serveCmd(args[1:])
	}
	return fmt.Errorf("queqiao router takes init, status, check, report or calibrate, not %q", args[0])
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
	var served []string // fork: what magpie's /v1/models would list (SP8 Task 10 asks magpie)
	for _, p := range provider.All() {
		for _, m := range p.Models {
			served = append(served, p.ID+"/"+m)
		}
	}
	resolved, unresolved := preset.Resolve(served)
	for _, m := range unresolved {
		fmt.Println(amber.Render("!"), "unresolved (no configured provider serves it yet):", m)
	}
	fillEmptyTiers(resolved)

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
		Thresholds: router.Thresholds{
			TierMin: 0.4, DissatisfiedMin: 0.7,
			ReviewMin: 0.7, ReviewConfidenceMin: 0.5,
		},
		Review:          router.ReviewConfig{Mode: router.ReviewOff, TimeoutMs: 5000, MaxAnswerChars: 6000},
		EscalateTurns:   2,
		CacheTTLSeconds: 300,
		FixedAgents: map[string]router.Tier{
			"Explore": router.TierFast, "statusline-setup": router.TierFast,
			"claude-code-guide": router.TierFast, "Plan": router.TierPerformance,
			"explorer": router.TierFast,
		},
		Experiment: router.ExperimentConfig{Enabled: false, RouterPercent: 50, ControlTier: router.TierPerformance, Salt: hex.EncodeToString(salt)},
		Listen:     "127.0.0.1:3426",
		MagpieURL:  "http://127.0.0.1:3425",
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
	if err := routerInitCodex(cfg.Listen); err != nil {
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

// routerInitCodex writes queqiao's Codex profile (~/.codex/queqiao.config.toml
// and its model catalog) pointing at queqiaod on listen (SP8 §5.6);
// config.toml is magpie's and the user's.
func routerInitCodex(listen string) error {
	changed, err := codexcfg.Init(codexHome(), "http://"+listen+"/v1")
	for _, f := range changed {
		fmt.Println(muted.Render("  write"), f)
	}
	if err != nil {
		return err
	}
	fmt.Println(amber.Render("!"), "start Codex with `codex -p queqiao` to use the router")
	return nil
}

// tierGroup names each tier's routing group (§4.4).
var tierGroup = map[router.Tier]string{
	router.TierFast: "qq-fast", router.TierBalanced: "qq-balanced", router.TierPerformance: "qq-perf",
}

// fillEmptyTiers gives a tier none of whose preset members resolved the
// members of its nearest tier (performance takes balanced's, then fast's;
// balanced takes performance's, then fast's; fast takes balanced's, then
// performance's), and says how to give it its own. A group must have a
// member, and borrowing keeps the tier working where an empty group would
// fail every request it gets (§7: a whole tier failing moves on anyway).
func fillEmptyTiers(resolved map[router.Tier][]string) {
	nearest := map[router.Tier][]router.Tier{
		router.TierPerformance: {router.TierBalanced, router.TierFast},
		router.TierBalanced:    {router.TierPerformance, router.TierFast},
		router.TierFast:        {router.TierBalanced, router.TierPerformance},
	}
	for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
		if len(resolved[tier]) > 0 {
			continue
		}
		for _, from := range nearest[tier] {
			if ms := resolved[from]; len(ms) > 0 {
				resolved[tier] = append([]string(nil), ms...)
				fmt.Println(amber.Render("!"), tierGroup[tier]+": none of the preset's models is served here; it uses", tierGroup[from]+"'s members for now ·",
					"queqiao group set "+tierGroup[tier]+" models=<provider>/<model>[,<provider>/<model>]")
				break
			}
		}
	}
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

// routerInitPi points Pi at the balanced tier (§4.5) exactly as
// `queqiao pi group/qq-balanced` does: settings.json's defaultProvider and
// defaultModel, the gateway provider in models.json and Pi's scope list,
// through the agent's own wiring. Without Pi here it writes nothing. It
// also takes out the top-level "magpie": {"default": …} an earlier router
// init put in models.json, which Pi never read.
func routerInitPi() error {
	dropStrayPiDefault(piModelsPath())
	a, err := agent.Find("pi")
	if err != nil || !a.Detected() {
		fmt.Println(muted.Render("  pi: not installed here, skipped · after installing it: queqiao pi group/qq-balanced"))
		return nil
	}
	return set(a, "model", "group/qq-balanced")
}

// dropStrayPiDefault removes models.json's top-level "magpie" when it is
// exactly what an earlier router init wrote there (an object holding one
// string "default"); any other value under that key is the user's.
func dropStrayPiDefault(path string) {
	raw, ok := edit.GetJSON(path, "magpie")
	if !ok {
		return
	}
	var v map[string]any
	if json.Unmarshal([]byte(raw), &v) != nil || len(v) != 1 {
		return
	}
	if d, ok := v["default"].(string); ok && strings.HasPrefix(d, "magpie/group/") {
		if err := edit.DelJSON(path, "magpie"); err == nil {
			fmt.Println(muted.Render("  edit"), path, "→ removed the unused top-level magpie.default")
		}
	}
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
	fmt.Println(muted.Render("  review:"), cfg.Review.Mode)
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

// routerCalibrate is `queqiao router calibrate [--since 14d]
// [--score tier|dissatisfied|review] [--harness h] [--agent main|sub]
// [--csv]` (SP7 §5.3).
func routerCalibrate(args []string) error {
	return routerCalibrateTo(os.Stdout, args)
}

// routerCalibrateTo is routerCalibrate with the output stream as a
// parameter (tests capture it).
func routerCalibrateTo(w io.Writer, args []string) error {
	since := 14 * 24 * time.Hour
	f := router.CalibrateFilter{}
	asCSV := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--since":
			i++
			if i >= len(args) {
				return fmt.Errorf("--since needs a duration like 14d")
			}
			d, err := parseSince(args[i])
			if err != nil {
				return fmt.Errorf("--since: %v", err)
			}
			since = d
		case "--score":
			i++
			if i >= len(args) {
				return fmt.Errorf("--score needs tier, dissatisfied or review")
			}
			if args[i] != "tier" && args[i] != "dissatisfied" && args[i] != "review" {
				return fmt.Errorf("--score takes tier, dissatisfied or review, not %q", args[i])
			}
			f.Score = args[i]
		case "--harness":
			i++
			if i >= len(args) {
				return fmt.Errorf("--harness needs a harness name")
			}
			f.Harness = args[i]
		case "--agent":
			i++
			if i >= len(args) {
				return fmt.Errorf("--agent needs main or sub")
			}
			if args[i] != "main" && args[i] != "sub" {
				return fmt.Errorf("--agent takes main or sub, not %q", args[i])
			}
			f.Agent = args[i]
		case "--csv":
			asCSV = true
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	cfg, err := router.Load(routerJSONPath(), "")
	if err != nil {
		return err
	}
	events, err := readRouterEvents()
	if err != nil {
		return err
	}
	f.Since = time.Now().Add(-since)
	if asCSV {
		router.CalibrateCSV(w, events, cfg, f)
		return nil
	}
	router.RenderCalibrate(w, router.Calibrate(events, cfg, f))
	return nil
}

// readRouterEvents reads router.jsonl, skipping lines that do not parse.
func readRouterEvents() ([]router.Event, error) {
	b, err := os.ReadFile(filepath.Join(appdir.Config(), "router.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var events []router.Event
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev router.Event
		if json.Unmarshal([]byte(line), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events, nil
}

// routerReport is `queqiao router report [--since 14d] [--json]` (§9):
// the experiment report over usage.jsonl and router.jsonl.
func routerReport(args []string) error {
	return routerReportTo(os.Stdout, args)
}

// routerReportTo is routerReport with the output stream as a parameter
// (tests capture it).
func routerReportTo(w io.Writer, args []string) error {
	since := 14 * 24 * time.Hour
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--since":
			i++
			if i >= len(args) {
				return fmt.Errorf("--since needs a duration like 14d")
			}
			d, err := parseSince(args[i])
			if err != nil {
				return fmt.Errorf("--since: %v", err)
			}
			since = d
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	// router.jsonl: one JSON event per line
	var events []router.Event
	if b, err := os.ReadFile(filepath.Join(appdir.Config(), "router.jsonl")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev router.Event
			if json.Unmarshal([]byte(line), &ev) == nil {
				events = append(events, ev)
			}
		}
	}

	// usage.jsonl via the ledger's block reader, only rows in the window
	now := time.Now()
	// fork: rows priced here as magpie prices cost_usd (SP8 Task 10 reads
	// `magpie usage --csv` instead)
	var records []magpie.UsageRow
	ledgerUsage.Visit(now.Add(-since), func(r ledgerUsage.Record) {
		if r.Session == "" {
			return
		}
		row := magpie.UsageRow{Time: r.Time, Session: r.Session, RequestedModel: r.Requested, Provider: r.Provider,
			Model: r.Model, Input: r.Input, Output: r.Output, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, Status: r.Status}
		if p, ok := catalog.PriceOf(r.Provider, r.Model); ok {
			row.CostUSD, row.Priced = p.Cost(r.Input, r.Output, r.CacheRead, r.CacheWrite), true
		}
		records = append(records, row)
	})

	states := router.ParallelPRStates(events, router.GhPRState, 4)
	rep := router.Aggregate(router.ReportInput{
		Records: records, Events: events, Now: now, Since: since,
		PRStates: states,
	})
	if asJSON {
		router.RenderJSON(w, rep)
	} else {
		router.Render(w, rep)
	}
	return nil
}

// parseSince reads "14d", "36h" or a plain time.ParseDuration.
func parseSince(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not a number of days", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	return d, nil
}
