package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
)

// routerHome gives a test a fresh config home and a provider serving the
// cn preset's fast model.
func routerHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := provider.Save(provider.Provider{
		ID: "deepseek", Name: "DeepSeek", Key: "k",
		Models: []string{"deepseek-v4-flash"}, Chat: "http://127.0.0.1:1/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{
		ID: "moonshot", Name: "Moonshot", Key: "k",
		Models: []string{"kimi-k2.5"}, Chat: "http://127.0.0.1:1/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{
		ID: "glm", Name: "GLM", Key: "k",
		Models: []string{"glm-5.3-flash", "glm-5.3"}, Chat: "http://127.0.0.1:1/v1",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRouterInitCnWritesGroupsAndConfig(t *testing.T) {
	routerHome(t)
	// cn preset: fast/balanced resolved by name; performance members are
	// <p>/… and resolve to nothing here — reported unresolved, left out of
	// the group, and the tier takes balanced's members for now
	// (TestRouterInitFillsAnEmptyTier) rather than failing init.
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	// four groups written
	for _, id := range []string{"qq-fast", "qq-balanced", "qq-perf", "queqiao"} {
		g, ok := groupByID(id)
		if !ok {
			t.Fatalf("group %s missing", id)
		}
		if len(g.Members) == 0 {
			t.Fatalf("group %s empty", id)
		}
	}
	g, _ := groupByID("queqiao")
	if g.Affinity != provider.AffinityTurn || g.Members[0] != "group/qq-balanced" {
		t.Fatalf("router group: %+v", g)
	}
	f, _ := groupByID("qq-fast")
	if f.Members[0] != "deepseek/deepseek-v4-flash" {
		t.Fatalf("fast members: %v", f.Members)
	}
	// router.json loads back as valid
	cfg, err := router.Load(routerJSONPath(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tiers[router.TierFast].Group != "qq-fast" || cfg.Classifier != "local" || cfg.ClassifyTimeoutMs != 1500 {
		t.Fatalf("cfg: %+v", cfg.Tiers[router.TierFast])
	}
	if cfg.Tiers[router.TierFast].Criteria == "" {
		t.Fatal("no criteria written")
	}
	if cfg.Experiment.Salt == "" {
		t.Fatal("no experiment salt")
	}
}

// A tier whose preset members nothing serves is not written with "<p>/"
// placeholders that can never answer: it takes the nearest tier's members
// (performance takes balanced's) and init says how to give it its own.
func TestRouterInitFillsAnEmptyTier(t *testing.T) {
	routerHome(t)
	out, err := captureStdout(t, func() error {
		return routerInit([]string{"--preset", "cn", "--groups-only"})
	})
	if err != nil {
		t.Fatal(err)
	}
	perf, _ := groupByID("qq-perf")
	bal, _ := groupByID("qq-balanced")
	for _, m := range perf.Members {
		if strings.HasPrefix(m, "<p>/") {
			t.Fatalf("qq-perf kept a placeholder: %v", perf.Members)
		}
	}
	if strings.Join(perf.Members, ",") != strings.Join(bal.Members, ",") {
		t.Fatalf("qq-perf = %v, want balanced's %v", perf.Members, bal.Members)
	}
	if !strings.Contains(out, "queqiao group set qq-perf models=") {
		t.Fatalf("init did not say how to give performance its own members:\n%s", out)
	}
}

func TestRouterInitRefusesOverwrite(t *testing.T) {
	routerHome(t)
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err == nil {
		t.Fatal("second init overwrote without --force")
	}
	if err := routerInit([]string{"--preset", "cn", "--groups-only", "--force"}); err != nil {
		t.Fatal(err)
	}
}

func TestRouterInitUnknownPreset(t *testing.T) {
	routerHome(t)
	if err := routerInit([]string{"--preset", "nope"}); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestRouterInitClaudeCodeEnv(t *testing.T) {
	routerHome(t)
	cwd := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	if err := routerInitClaudeCode(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Env["ANTHROPIC_MODEL"] != "group/queqiao" || s.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "group/qq-fast" {
		t.Fatalf("env: %v", s.Env)
	}
}

// router init's Codex step writes queqiao's own profile file and leaves
// config.toml to magpie (SP8 §5.6).
func TestRouterInitCodexWritesProfile(t *testing.T) {
	dir := t.TempDir()
	old := codexHome
	codexHome = func() string { return dir }
	t.Cleanup(func() { codexHome = old })
	existing := "[model_providers.magpie]\nname = \"magpie\"\nbase_url = \"http://127.0.0.1:3425/v1\"\n"
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(existing), 0o644)
	if err := routerInitCodex("127.0.0.1:3426"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.toml")); string(b) != existing {
		t.Fatalf("config.toml changed:\n%s", b)
	}
	b, err := os.ReadFile(filepath.Join(dir, "queqiao.config.toml"))
	if err != nil || !strings.Contains(string(b), `base_url = "http://127.0.0.1:3426/v1"`) {
		t.Fatalf("profile %s (%v)", b, err)
	}
}

func TestRouterStatusOffline(t *testing.T) {
	routerHome(t)
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	// no gateway serving on a random port
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:1")
	if err := routerStatus(nil); err != nil {
		t.Fatal(err)
	}
}

func TestRouterStatusInvalidConfig(t *testing.T) {
	routerHome(t)
	p := filepath.Join(appdir.Config(), "router.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"tiers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := routerStatus(nil); err != nil {
		t.Fatal(err) // status reports invalid, it does not fail the command
	}
}

// ---- SP5: router report ----

// synthReportFiles writes synthetic router.jsonl and usage.jsonl under a
// temp config home and points the process there.
func synthReportFiles(t *testing.T, events []string, usageRows []string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := appdir.Config() // the test process' app name, not main's
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg+"/router.jsonl", []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg+"/usage.jsonl", []byte(strings.Join(usageRows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRouterReportOverSyntheticFiles(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	synthReportFiles(t,
		[]string{
			`{"kind":"decide","t":"` + at(3*time.Hour) + `","session":"r1","harness":"codex","tier":"fast","reason":"R5","arm":"router"}`,
			`{"kind":"shadow","t":"` + at(3*time.Hour) + `","session":"c1","harness":"codex","tier":"performance","reason":"R5","arm":"control","shadow_tier":"balanced"}`,
			`{"kind":"hint_consumed","t":"` + at(2*time.Hour) + `","session":"r1"}`,
			`{"kind":"feedback","t":"` + at(time.Hour) + `","session":"r1","extra":"pr_created https://github.com/a/b/pull/1"}`,
			`{"kind":"feedback","t":"` + at(30*time.Minute) + `","session":"c1","extra":"manual_model_switch other/m1"}`,
		},
		[]string{
			`{"t":"` + at(2*time.Hour) + `","agent":"codex","provider":"a","model":"fastm","session":"r1","status":200,"in":1000,"out":1000}`,
			`{"t":"` + at(2*time.Hour) + `","agent":"codex","provider":"b","model":"balm","session":"c1","status":200,"in":2000,"out":1000}`,
		})

	var buf bytes.Buffer
	if err := routerReportTo(&buf, []string{"--json", "--since", "7d"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`"sessions": 1`, `"merged_z"`, `"router"`, `"control"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}

	// the text form shows the two arms side by side
	buf.Reset()
	if err := routerReportTo(&buf, []string{"--since", "7d"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "queqiao router report") ||
		!strings.Contains(buf.String(), "样本不足") {
		t.Fatalf("text report:\n%s", buf.String())
	}
}

func TestRouterReportRejectsBadFlags(t *testing.T) {
	if err := routerReport([]string{"--since", "week"}); err == nil {
		t.Fatal("bad duration accepted")
	}
	if err := routerReport([]string{"--nope"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

// frontier with none of its gpt or claude models served: balanced and
// performance both take fast's members, no placeholder left anywhere.
func TestRouterInitFrontierWithOnlyFastServed(t *testing.T) {
	routerHome(t)
	if _, err := captureStdout(t, func() error {
		return routerInit([]string{"--preset", "frontier", "--groups-only"})
	}); err != nil {
		t.Fatal(err)
	}
	fast, _ := groupByID("qq-fast")
	for _, id := range []string{"qq-balanced", "qq-perf"} {
		g, _ := groupByID(id)
		if strings.Join(g.Members, ",") != strings.Join(fast.Members, ",") {
			t.Fatalf("%s = %v, want fast's %v", id, g.Members, fast.Members)
		}
	}
	for _, m := range fast.Members {
		if strings.HasPrefix(m, "<p>/") {
			t.Fatalf("qq-fast kept a placeholder: %v", fast.Members)
		}
	}
}

// router init points Pi at the balanced tier the way `queqiao pi` does:
// settings.json's defaultProvider/defaultModel and the gateway provider in
// models.json, the user's other settings kept. It also takes out the
// top-level "magpie": {"default": …} an earlier router init wrote into
// models.json, which Pi never read.
func TestRouterInitPiSetsPisDefaultModel(t *testing.T) {
	routerHome(t)
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme": "dark"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"magpie": {"default": "magpie/group/qq-balanced"}, "providers": {}}`), 0o644)
	if _, err := captureStdout(t, routerInitPi); err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["defaultProvider"] != "magpie" || settings["defaultModel"] != "group/qq-balanced" || settings["theme"] != "dark" {
		t.Fatalf("settings.json: %s", b)
	}
	var models map[string]json.RawMessage
	b, _ = os.ReadFile(filepath.Join(dir, "models.json"))
	if err := json.Unmarshal(b, &models); err != nil {
		t.Fatal(err)
	}
	if _, stray := models["magpie"]; stray {
		t.Fatalf("models.json kept the stray top-level magpie: %s", b)
	}
	var providers map[string]json.RawMessage
	json.Unmarshal(models["providers"], &providers)
	if _, ok := providers["magpie"]; !ok {
		t.Fatalf("models.json has no gateway provider: %s", b)
	}
}

// A top-level "magpie" in models.json that isn't the one router init
// wrote (any other shape) is the user's: it stays.
func TestRouterInitPiKeepsAnotherTopLevelMagpie(t *testing.T) {
	routerHome(t)
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".pi", "agent")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o644)
	os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"magpie": {"note": "mine"}, "providers": {}}`), 0o644)
	if _, err := captureStdout(t, routerInitPi); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if !strings.Contains(string(b), `"note": "mine"`) && !strings.Contains(string(b), `"note":"mine"`) {
		t.Fatalf("the user's own top-level magpie went: %s", b)
	}
}

// Without Pi on this machine router init leaves no Pi files behind.
func TestRouterInitPiWithoutPi(t *testing.T) {
	routerHome(t)
	t.Setenv("PATH", t.TempDir())
	if _, err := captureStdout(t, routerInitPi); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".pi")); err == nil {
		t.Fatal("router init made ~/.pi with no Pi here")
	}
}

// SP7: init writes the review defaults so a fresh config has them visible.
func TestRouterInitWritesReviewDefaults(t *testing.T) {
	routerHome(t)
	if err := routerInit([]string{"--preset", "cn", "--groups-only"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(routerJSONPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"review"`, `"mode": "off"`, `"review_min": 0.7`, `"review_confidence_min": 0.5`, `"timeout_ms": 5000`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("router.json lacks %s:\n%s", want, raw)
		}
	}
}

// writeCalibrateConfig writes a valid router.json beside the synthetic
// event log, since calibrate reads the current thresholds from it.
func writeCalibrateConfig(t *testing.T) {
	t.Helper()
	cfg := appdir.Config()
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"tiers":{"fast":{"group":"g","claude_alias":"f"},"balanced":{"group":"g","claude_alias":"b"},"performance":{"group":"g","claude_alias":"p"}}}`
	if err := os.WriteFile(filepath.Join(cfg, "router.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRouterCalibrateCSV(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	synthReportFiles(t,
		[]string{
			`{"kind":"decide","t":"` + at(3*time.Hour) + `","session":"r1","harness":"codex","agent":"main","arm":"router","turn_id":"t1","tier":"fast","classified_tier":"fast","tier_confidence":0.31,"reason":"R5"}`,
			`{"kind":"decide","t":"` + at(time.Hour) + `","session":"r1","harness":"codex","agent":"main","arm":"router","turn_id":"t2","tier":"balanced","classified_tier":"fast","tier_confidence":0.95,"dissatisfied":0.9,"reason":"R3-review"}`,
		}, nil)
	writeCalibrateConfig(t)

	var buf bytes.Buffer
	if err := routerCalibrateTo(&buf, []string{"--csv"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	want := "session,turn_id,harness,agent,tier,tier_confidence,dissatisfied,unresolved,under_tiered"
	if lines[0] != want {
		t.Fatalf("header = %q, want %q", lines[0], want)
	}
	if len(lines) != 3 {
		t.Fatalf("rows = %d, want 2:\n%s", len(lines)-1, buf.String())
	}
	if !strings.HasPrefix(lines[1], "r1,t1,codex,main,fast,0.310,") {
		t.Fatalf("first row: %q", lines[1])
	}
	for _, l := range lines[1:] {
		if strings.Contains(l, " ") {
			t.Fatalf("CSV row carries prose: %q", l)
		}
	}
}

func TestRouterCalibrateText(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	synthReportFiles(t,
		[]string{`{"kind":"decide","t":"` + at(time.Hour) + `","session":"r1","harness":"codex","agent":"main","arm":"router","turn_id":"t1","tier":"fast","classified_tier":"fast","tier_confidence":0.31,"reason":"R5"}`},
		nil)
	writeCalibrateConfig(t)
	var buf bytes.Buffer
	if err := routerCalibrateTo(&buf, []string{"--score", "tier"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"标签只反映用户表达出来的不满", "档位置信度", "升档率", "未升档轮次的选低率"} {
		if !strings.Contains(out, want) {
			t.Fatalf("calibrate text missing %q:\n%s", want, out)
		}
	}
	if err := routerCalibrateTo(&buf, []string{"--score", "nope"}); err == nil {
		t.Fatal("--score nope was accepted")
	}
}
