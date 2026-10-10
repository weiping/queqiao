//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/weiping/magpie-bridge/internal/testmagpie"
)

// The author's case (2026-10-10): the four groups were renamed by hand with
// tuned members and router.json was copied over, then `mbridge router init`
// runs to wire the agents. Against official magpie, init must leave the
// groups' members and router.json as they are.
func TestE2EInitKeepsTunedSetup(t *testing.T) {
	up := testmagpie.NewUpstream(t)
	m := testmagpie.Start(t, testmagpie.Bin(t), up, "m-fast", "m-bal", "m-perf")
	for _, g := range [][2]string{
		{"mb-fast", "fake/m-fast"}, {"mb-balanced", "fake/m-bal"}, {"mb-perf", "fake/m-perf"},
		{"mbridge", "group/mb-balanced,group/mb-perf,group/mb-fast"},
	} {
		m.CLI(t, "group", "add", g[0], "models="+g[1], "routing=order")
	}
	before := groupsOf(m.CLI(t, "groups"))

	// mbridge on PATH beside a "magpie" that is official magpie
	bin := t.TempDir()
	exe := filepath.Join(bin, "mbridge")
	mag := filepath.Join(bin, "magpie")
	if runtime.GOOS == "windows" {
		exe, mag = exe+".exe", mag+".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, "../cmd/mbridge").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if err := os.Symlink(m.Bin, mag); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(m.Home, ".config", "magpie-bridge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tuned := `{"version":1,"router_group":"mbridge","default_tier":"balanced","classifier":"typesafe/jev-latest",
  "classify_timeout_ms":3000,"magpie_url":` + jsonStr(m.URL) + `,"listen":"127.0.0.1:3426",
  "tiers":{"fast":{"group":"mb-fast","claude_alias":"haiku","criteria":"tuned fast"},
           "balanced":{"group":"mb-balanced","claude_alias":"sonnet","criteria":"tuned balanced"},
           "performance":{"group":"mb-perf","claude_alias":"opus","criteria":"tuned perf"}}}
`
	if err := os.WriteFile(filepath.Join(cfgDir, "router.json"), []byte(tuned), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exe, "router", "init", "--preset", "cn")
	cmd.Dir = t.TempDir()
	cmd.Env = append(m.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "MBRIDGE_CONFIG_DIR="+cfgDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	if after := groupsOf(m.CLI(t, "groups")); after != before {
		t.Fatalf("init changed magpie's groups:\nbefore:\n%s\nafter:\n%s\ninit said:\n%s", before, after, out)
	}
	if b, _ := os.ReadFile(filepath.Join(cfgDir, "router.json")); string(b) != tuned {
		t.Fatalf("init rewrote router.json:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(m.Home, ".codex", "mbridge.config.toml")); err != nil {
		t.Fatalf("Codex profile not written: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "kept") {
		t.Fatalf("init did not say what it kept:\n%s", out)
	}
}

// groupsOf is `magpie groups` without the "← Pi" markers that say which
// agent uses a group (init points Pi at mb-balanced; that is not a change
// to the group).
func groupsOf(out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "←"); i >= 0 {
			l = l[:i]
		}
		lines = append(lines, strings.TrimRight(l, " "))
	}
	return strings.Join(lines, "\n")
}
