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

func TestCodexConfigKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// an existing config with unrelated content keeps it
	existing := "[model_providers.magpie]\nname = \"magpie\"\nbase_url = \"http://127.0.0.1:3425/v1\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := codexConfigKeys(path, dir+"/queqiao-models.json"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model = "group/queqiao"`,
		`model_provider = "magpie"`,
		"[model_providers.magpie]",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	// keys land before the first table
	if strings.Index(s, "model = ") > strings.Index(s, "[model_providers") {
		t.Fatal("model key landed inside a table")
	}
	// idempotent: second run changes nothing
	if err := codexConfigKeys(path, dir+"/queqiao-models.json"); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != s {
		t.Fatal("not idempotent")
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
