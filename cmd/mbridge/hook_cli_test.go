package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hook_cli drives the built binary end to end: stdin in, exit code and
// stdout out — the exact contract Codex's hook runner sees.
func runHook(t *testing.T, args ...string) (int, string) {
	t.Helper()
	bin := buildMbridge(t)
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(hookStdin)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("could not run %s: %v", bin, err) // not an exit code: no answer at all
	}
	return code, string(out)
}

const hookStdin = `{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t1","prompt":"hi","model":"group/mbridge"}`

func TestHookCommandExitsZeroWhenGatewayIsDown(t *testing.T) {
	t.Setenv("MBRIDGE_URL", "http://127.0.0.1:1")
	code, out := runHook(t, "hook", "user-prompt", "--harness", "codex")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout not empty: %q", out)
	}
}

func TestHookCommandRejectsUnknownsButStaysSilentOnKnown(t *testing.T) {
	t.Setenv("MBRIDGE_URL", "http://127.0.0.1:1")
	code, out := runHook(t, "hook", "nonsense")
	t.Logf("nonsense: code=%d out=%q", code, out)
	if code == 0 {
		t.Fatal("unknown subcommand accepted")
	}
	if code, _ := runHook(t, "hook", "post-bash"); code != 0 {
		t.Fatal("known subcommand failed")
	}
}

// buildMbridge builds the cli binary once per test run, in a dir that
// outlives the individual test (a t.TempDir would delete it).
var buildMbridge = func(t *testing.T) string {
	t.Helper()
	if buildMbridgeCached != "" {
		if _, err := os.Stat(buildMbridgeCached); err == nil {
			return buildMbridgeCached
		}
	}
	dir, err := os.MkdirTemp("", "mbridge-hook-test-")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "mbridge-test")
	if runtime.GOOS == "windows" {
		bin += ".exe" // Windows starts nothing without its extension
	}
	cmd := exec.Command("go", "build", "-tags", "nogui", "-o", bin, ".")
	// TestMain's sandbox swaps HOME, which would give this build a cold
	// module and build cache every run: every module re-downloaded, the
	// whole tree recompiled. A stable folder under the OS temp dir keeps
	// the isolation (nothing of the user's cache is written) while making
	// reruns download once and build from cache.
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+filepath.Join(os.TempDir(), "mbridge-hook-test-modcache"),
		"GOCACHE="+filepath.Join(os.TempDir(), "mbridge-hook-test-buildcache"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	buildMbridgeCached = bin
	return bin
}

var buildMbridgeCached string
