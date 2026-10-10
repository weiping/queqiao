package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/weiping/magpie-bridge/internal/codexcfg"
	"github.com/weiping/magpie-bridge/internal/fsutil"
	"github.com/weiping/magpie-bridge/internal/magpie"
	"github.com/weiping/magpie-bridge/internal/router"
)

// newMagpie makes the client mbridge talks to official magpie with
// (tests replace it with a fake).
var newMagpie = magpie.New

// magpieFor is the client for router.json's magpie_url, or the default
// when router.json can't be read.
func magpieFor() *magpie.Client {
	url := magpie.DefaultURL
	if cfg, err := router.Load(routerJSONPath(), ""); err == nil {
		url = cfg.MagpieURL
	}
	return newMagpie(url)
}

// routerCmd is `mbridge router <init|status|check>` (spec §6.6).
func routerCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("mbridge router takes init, status, check, report or calibrate")
	}
	switch args[0] {
	case "init":
		return routerInit(args[1:])
	case "status": // `mbridge status` is the same
		return statusCmd(args[1:])
	case "check":
		return routerCheck(args[1:])
	case "report":
		return routerReport(args[1:])
	case "calibrate":
		return routerCalibrate(args[1:])
	case "serve": // the fork's spelling, kept as an alias of `mbridge serve`
		return serveCmd(args[1:])
	}
	return fmt.Errorf("mbridge router takes init, status, check, report or calibrate, not %q", args[0])
}

// routerJSONPath is ~/.config/magpie-bridge/router.json.
func routerJSONPath() string {
	return filepath.Join(fsutil.ConfigDir(), "router.json")
}

// criteria are §4.1's defaults, written by init so projects can override
// them in .mbridge/router.json.
var defaultCriteria = map[router.Tier]string{
	router.TierFast:        "Little work: a question, an explanation, reading logs, running a command, a one-line or mechanical change, a read-only code search",
	router.TierBalanced:    "Some work: an ordinary bug fix or a small feature in code already understood, adding tests, a single-file refactor",
	router.TierPerformance: "Much work: a change across several files, a bug whose cause is unknown, concurrency, performance or security issues, an architecture or design decision, a long multi-step plan",
}

// routerInit is `mbridge router init --preset <name> [--groups-only]
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
	mc := magpieFor()
	ctx := context.Background()
	served, err := mc.Models(ctx)
	if err != nil {
		return fmt.Errorf("%v — start magpie first (magpie serve, or its app)", err)
	}
	resolved, unresolved := preset.Resolve(served)
	for _, m := range unresolved {
		fmt.Println(amber.Render("!"), "unresolved (no configured provider serves it yet):", m)
	}
	fillEmptyTiers(resolved)

	// 1) the four routing groups (§4.4), made by magpie's own CLI
	groups := []struct {
		id      string
		members []string
	}{
		{"mb-fast", resolved[router.TierFast]},
		{"mb-balanced", resolved[router.TierBalanced]},
		{"mb-perf", resolved[router.TierPerformance]},
		{"mbridge", []string{"group/mb-balanced", "group/mb-perf", "group/mb-fast"}},
	}
	// a group already in magpie is the user's (tuned members, a rename by
	// hand): init keeps it unless --force. Not knowing which exist is not
	// "none exist".
	have := map[string]bool{}
	if !force {
		ids, err := mc.Groups(ctx)
		if err != nil {
			return fmt.Errorf("reading magpie's groups: %v", err)
		}
		for _, id := range ids {
			have[id] = true
		}
	}
	for _, g := range groups {
		if !have[g.id] && len(g.members) == 0 {
			return fmt.Errorf("group %s has no members (preset %q): add a provider serving one to magpie first", g.id, presetID)
		}
	}
	for _, g := range groups {
		if have[g.id] {
			fmt.Println(muted.Render("  kept"), "group", g.id, muted.Render("(already in magpie; --force puts the preset's members back)"))
			continue
		}
		if err := mc.GroupAdd(ctx, g.id, g.members); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "group", g.id, "→", strings.Join(g.members, ", "))
	}

	// 2) router.json (§4.6), §4.1's default criteria, classifier local
	p := routerJSONPath()
	keepJSON := false
	if _, err := os.Stat(p); err == nil && !force {
		keepJSON = true
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	salt := make([]byte, 8)
	_, _ = rand.Read(salt)
	cfg := router.Config{
		Version:     1,
		RouterGroup: "mbridge",
		Tiers: map[router.Tier]router.TierCfg{
			router.TierFast:        {Group: "mb-fast", ClaudeAlias: "haiku", Criteria: defaultCriteria[router.TierFast]},
			router.TierBalanced:    {Group: "mb-balanced", ClaudeAlias: "sonnet", Criteria: defaultCriteria[router.TierBalanced]},
			router.TierPerformance: {Group: "mb-perf", ClaudeAlias: "opus", Criteria: defaultCriteria[router.TierPerformance]},
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
	if keepJSON {
		// the agents below are wired to where the user's file listens
		if have, err := router.Load(p, ""); err == nil && have.Listen != "" {
			cfg.Listen = have.Listen
		}
		fmt.Println(muted.Render("  kept"), p, muted.Render("(--force writes the defaults over it)"))
	} else {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := fsutil.WriteAtomic(p, append(b, '\n')); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "wrote", p)
	}

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

// routerInitCodex writes mbridge's Codex profile (~/.codex/mbridge.config.toml
// and its model catalog) pointing at mbridge on listen (SP8 §5.6);
// config.toml is magpie's and the user's.
func routerInitCodex(listen string) error {
	changed, err := codexcfg.Init(codexHome(), "http://"+listen+"/v1")
	for _, f := range changed {
		fmt.Println(muted.Render("  write"), f)
	}
	if err != nil {
		return err
	}
	fmt.Println(amber.Render("!"), "start Codex with `codex -p mbridge` to use the router")
	return nil
}

// tierGroup names each tier's routing group (§4.4).
var tierGroup = map[router.Tier]string{
	router.TierFast: "mb-fast", router.TierBalanced: "mb-balanced", router.TierPerformance: "mb-perf",
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
					"magpie group set "+tierGroup[tier]+" models=<provider>/<model>[,<provider>/<model>]")
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
	return fsutil.SetJSON(path,
		fsutil.KV{Path: "env.ANTHROPIC_MODEL", Value: "group/mbridge"},
		fsutil.KV{Path: "env.ANTHROPIC_DEFAULT_HAIKU_MODEL", Value: "group/mb-fast"},
		fsutil.KV{Path: "env.ANTHROPIC_SMALL_FAST_MODEL", Value: "group/mb-fast"},
		fsutil.KV{Path: "env.ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "group/mb-balanced"},
		fsutil.KV{Path: "env.ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "group/mb-perf"},
		fsutil.KV{Path: "env.ANTHROPIC_DEFAULT_FABLE_MODEL", Value: "group/mb-perf"},
	)
}

// piModelsPath is ~/.pi/agent/models.json (overridable in tests).
var piModelsPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent", "models.json")
}

// routerInitPi points Pi at the balanced tier (§4.5) through magpie's own
// wiring, `magpie pi group/mb-balanced`; without Pi, magpie says so and
// init goes on. It also takes out the top-level "magpie": {"default": …}
// an earlier router init put in models.json, which Pi never read.
func routerInitPi() error {
	dropStrayPiDefault(piModelsPath())
	if err := magpieFor().SetAgentModel(context.Background(), "pi", "group/mb-balanced"); err != nil {
		fmt.Println(muted.Render("  pi: " + err.Error() + " · after installing Pi: magpie pi group/mb-balanced"))
		return nil
	}
	fmt.Println(green.Render("✓"), "pi → group/mb-balanced")
	return nil
}

// dropStrayPiDefault removes models.json's top-level "magpie" when it is
// exactly what an earlier router init wrote there (an object holding one
// string "default"); any other value under that key is the user's.
func dropStrayPiDefault(path string) {
	raw, ok, err := fsutil.GetJSON(path, "magpie")
	if err != nil || !ok {
		return
	}
	var v map[string]any
	if json.Unmarshal([]byte(raw), &v) != nil || len(v) != 1 {
		return
	}
	if d, ok := v["default"].(string); ok && strings.HasPrefix(d, "magpie/group/") {
		if err := fsutil.DelJSON(path, "magpie"); err == nil {
			fmt.Println(muted.Render("  edit"), path, "→ removed the unused top-level magpie.default")
		}
	}
}

// routerCheck is `mbridge router check [--yes]`: §4.6's smoke test. The
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
	// structural: every tier group is one magpie serves, in a big enough
	// window (§4.2 #4: fast ≥128k, balanced ≥200k, performance ≥ the other
	// two's), as magpie's /v1/models says it
	listed, err := magpieFor().ModelList(context.Background())
	if err != nil {
		fmt.Println(amber.Render("✗"), err)
		return fmt.Errorf("router check failed")
	}
	byID := map[string]magpie.Model{}
	for _, m := range listed {
		byID[m.ID] = m
	}
	windows := map[router.Tier]int{router.TierFast: 128000, router.TierBalanced: 200000}
	tierMax := map[router.Tier]int{}
	for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
		id := magpie.GroupPrefix + cfg.Tiers[tier].Group
		m, ok := byID[id]
		if !ok {
			fmt.Println(amber.Render("✗"), tier, "group missing in magpie:", id)
			failed = true
			continue
		}
		if w := m.Context; w > 0 {
			if tier != router.TierPerformance && w < windows[tier] {
				fmt.Println(amber.Render("✗"), tier, "window too small:", id, w)
				failed = true
			}
			tierMax[tier] = w
		}
		fmt.Println(green.Render("✓"), tier, "group", id)
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

// routerCheckLive is §4.6's billable half: 20 tool-carrying requests per
// primary and failover member, over Anthropic Messages and OpenAI
// Responses, through the running gateway so the real translation path is
// exercised. A member fails the gate when tool-argument parse fails more
// than once in 20.
func routerCheckLive(cfg router.Config) error {
	if err := newMagpie(cfg.MagpieURL).Health(context.Background()); err != nil {
		return fmt.Errorf("live check needs magpie running: %v", err)
	}
	fmt.Println(amber.Render("!"), "live check: real requests to configured providers, billed to them")
	for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
		// the tier's group as agents reach it: magpie fails over within it
		m := magpie.GroupPrefix + cfg.Tiers[tier].Group
		for _, proto := range []string{"anthropic", "responses"} {
			if err := routerProbeMember(cfg.MagpieURL, m, proto); err != nil {
				return fmt.Errorf("%s (%s over %s): %v", tier, m, proto, err)
			}
		}
		fmt.Println(green.Render("✓"), "live:", tier, m)
	}
	return nil
}

// routerProbeMember sends 20 tool-carrying requests for member over proto
// and fails when the tool call comes back unparseable more than once.
func routerProbeMember(base, member, proto string) error {
	var url, body string
	if proto == "anthropic" {
		url = base + "/v1/messages"
		body = fmt.Sprintf(`{"model":%q,"max_tokens":64,"tools":[{"name":"echo","description":"echo","input_schema":{"type":"object","properties":{"text":{"type":"string"}}}}],"tool_choice":{"type":"tool","name":"echo"},"messages":[{"role":"user","content":"call echo with text probe"}]}`, member)
	} else {
		url = base + "/v1/responses"
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

// routerCalibrate is `mbridge router calibrate [--since 14d]
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
	b, err := os.ReadFile(filepath.Join(fsutil.ConfigDir(), "router.jsonl"))
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

// routerReport is `mbridge router report [--since 14d] [--json]` (§9):
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
	if b, err := os.ReadFile(filepath.Join(fsutil.ConfigDir(), "router.jsonl")); err == nil {
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

	// magpie's ledger (`magpie usage --csv`), its own cost included
	now := time.Now()
	period := "all"
	switch {
	case since <= 7*24*time.Hour:
		period = "7d"
	case since <= 30*24*time.Hour:
		period = "30d"
	}
	rows, err := magpieFor().Usage(context.Background(), period)
	if err != nil {
		return err
	}
	var records []magpie.UsageRow
	for _, r := range rows {
		if r.Session != "" {
			records = append(records, r)
		}
	}

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
