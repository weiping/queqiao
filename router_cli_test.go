package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// <p>/… and resolve to nothing here — left literal, and reported
	// unresolved rather than failing init.
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
