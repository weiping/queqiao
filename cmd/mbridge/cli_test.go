package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/weiping/magpie-bridge/internal/service"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/weiping/magpie-bridge/internal/fsutil"
)

func TestNoMagpieDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../../...").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(string(out), "yetone/magpie") {
		t.Fatalf("depends on magpie:\n%s", out)
	}
}

func TestCLIListsOnlyItsCommands(t *testing.T) {
	for _, want := range []string{"mbridge serve", "mbridge service", "mbridge status",
		"mbridge router init", "mbridge router report", "mbridge router calibrate", "mbridge hook", "mbridge update", "mbridge version"} {
		if !strings.Contains(usageText, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, gone := range []string{"migrate", "mbridge provider", "mbridge group ", "mbridge tui", "mbridge web", "mbridge accounts"} {
		if strings.Contains(usageText, gone) {
			t.Errorf("help still has magpie's %q", gone)
		}
	}
	if code := run([]string{"provider", "add"}); code == 0 {
		t.Fatal("a magpie command was accepted")
	}
}

// writeRouterJSON writes a valid router.json with mbridge at listen.
func writeRouterJSON(t *testing.T, listen string) {
	t.Helper()
	body := fmt.Sprintf(`{"version":1,"router_group":"mbridge","tiers":{"fast":{"group":"mb-fast","claude_alias":"haiku"},"balanced":{"group":"mb-balanced","claude_alias":"sonnet"},"performance":{"group":"mb-perf","claude_alias":"opus"}},"listen":%q}`, listen)
	os.MkdirAll(fsutil.ConfigDir(), 0o755)
	if err := os.WriteFile(routerJSONPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeDaemon(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bridge/router" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"valid": true, "decisions": []map[string]any{
			{"session": "s-1", "tier": "fast", "reason": "R5-classified"}}})
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestStatusReportsMagpieAndDaemon(t *testing.T) {
	t.Run("all up, a group missing", func(t *testing.T) {
		f := withFakeMagpie(t)
		f.groups["mb-fast"], f.groups["mb-balanced"] = []string{"a/x"}, []string{"a/y"}
		f.order = []string{"mb-fast", "mb-balanced"}
		writeRouterJSON(t, fakeDaemon(t))
		out, err := captureStdout(t, func() error { return statusCmd(nil) })
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"magpie v0.1.1100", "mbridge", "group/mb-perf missing", "s-1 → fast (R5-classified)"} {
			if !strings.Contains(out, want) {
				t.Fatalf("status lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("magpie down", func(t *testing.T) {
		f := withFakeMagpie(t)
		f.down = true
		writeRouterJSON(t, fakeDaemon(t))
		out, _ := captureStdout(t, func() error { return statusCmd(nil) })
		if !strings.Contains(out, "magpie isn't answering") {
			t.Fatalf("status:\n%s", out)
		}
	})
	t.Run("mbridge down", func(t *testing.T) {
		withFakeMagpie(t)
		writeRouterJSON(t, "127.0.0.1:1")
		out, _ := captureStdout(t, func() error { return statusCmd(nil) })
		if !strings.Contains(out, "mbridge isn't running on 127.0.0.1:1") {
			t.Fatalf("status:\n%s", out)
		}
	})
	t.Run("invalid router.json", func(t *testing.T) {
		withFakeMagpie(t)
		os.MkdirAll(fsutil.ConfigDir(), 0o755)
		os.WriteFile(routerJSONPath(), []byte(`{"tiers":{}}`), 0o644)
		out, err := captureStdout(t, func() error { return statusCmd(nil) })
		if err != nil || !strings.Contains(out, "router.json") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
}

func TestVersionShowsMagpieVersion(t *testing.T) {
	withFakeMagpie(t)
	out, _ := captureStdout(t, func() error { return versionCmd(nil) })
	if !strings.Contains(out, "mbridge "+version) || !strings.Contains(out, "magpie v0.1.1100") {
		t.Fatalf("version:\n%s", out)
	}
}

// fakeReleases serves a GitHub releases list with one v release whose
// asset for this platform is body, and checksums.txt saying sum.
func fakeReleases(t *testing.T, body, sum string) string {
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/weiping/magpie-bridge/releases":
			json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": "nightly-9.9.9", "assets": []any{}},
				{"tag_name": "v0.2.0", "assets": []map[string]any{
					{"name": asset, "browser_download_url": srv.URL + "/dl/" + asset},
					{"name": "checksums.txt", "browser_download_url": srv.URL + "/dl/checksums.txt"},
				}},
			})
		case "/dl/" + asset:
			io.WriteString(w, body)
		case "/dl/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", sum, asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestUpdateVerifiesChecksum(t *testing.T) {
	restarted := false
	svcHome := t.TempDir()
	old := newService
	newService = func() *service.Manager {
		m := &service.Manager{GOOS: "linux", Home: svcHome, ConfigDir: svcHome,
			Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if strings.Join(args, " ") == "--user restart mbridge.service" {
					restarted = true
				}
				return nil, nil
			}}
		path, content := service.Unit("linux", svcHome, svcHome, "/q")
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, content, 0o644)
		return m
	}
	t.Cleanup(func() { newService = old })
	exe := filepath.Join(t.TempDir(), "mbridge")
	os.WriteFile(exe, []byte("old"), 0o755)
	api := fakeReleases(t, "new binary", strings.Repeat("0", 64))
	if err := updateFrom(api, exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("replaced despite a bad checksum")
	}
	sum := sha256.Sum256([]byte("new binary"))
	api = fakeReleases(t, "new binary", hex.EncodeToString(sum[:]))
	if err := updateFrom(api, exe); err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("the running mbridge was not restarted onto the new binary")
	}
	b, _ := os.ReadFile(exe)
	st, _ := os.Stat(exe)
	// Windows runs any .exe: there is no execute bit to set
	if string(b) != "new binary" || runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("%q %v", b, st.Mode())
	}
}

// `mbridge service status` says what the service manager says, and an
// uninstalled service is not an error.
func TestServiceStatusNotInstalled(t *testing.T) {
	home := t.TempDir()
	old := newService
	newService = func() *service.Manager {
		return &service.Manager{GOOS: "linux", Home: home, ConfigDir: home,
			Run: func(ctx context.Context, name string, args ...string) ([]byte, error) { return nil, nil }}
	}
	t.Cleanup(func() { newService = old })
	out, err := captureStdout(t, func() error { return serviceCmd([]string{"status"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not installed") {
		t.Fatalf("status said %q", out)
	}
}

// 10-11: mb-perf's two Copilot members were skipped (Copilot off), magpie
// dropped the group from /v1/models, and router check said only "group
// missing in magpie". It says why, and how to fix it.
func TestRouterCheckSaysWhyAGroupIsntServed(t *testing.T) {
	copilotOff := func(t *testing.T) *fakeMag {
		f := withFakeMagpie(t)
		f.groups["mb-fast"], f.groups["mb-balanced"] = []string{"a/x"}, []string{"a/y"}
		f.groups["mb-perf"] = []string{"copilot/gpt-6-astra:low", "copilot/claude-opus-5.5"}
		f.unserved["copilot/gpt-6-astra:low"] = "Copilot is off now, skipped"
		f.unserved["copilot/claude-opus-5.5"] = "Copilot is off now, skipped"
		f.order = []string{"mb-fast", "mb-balanced", "mb-perf"}
		return f
	}
	t.Run("router check", func(t *testing.T) {
		copilotOff(t)
		writeRouterJSON(t, "127.0.0.1:1")
		out, err := captureStdout(t, func() error { return routerCheck(nil) })
		if err == nil {
			t.Fatalf("check passed:\n%s", out)
		}
		for _, want := range []string{
			"performance group group/mb-perf: magpie serves none of its models",
			"copilot/gpt-6-astra:low: Copilot is off now, skipped",
			"copilot/claude-opus-5.5: Copilot is off now, skipped",
			"magpie group set mb-perf models=",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("check lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "missing") {
			t.Fatalf("an unserved group called missing:\n%s", out)
		}
	})
	t.Run("status", func(t *testing.T) {
		copilotOff(t)
		writeRouterJSON(t, fakeDaemon(t))
		out, _ := captureStdout(t, func() error { return statusCmd(nil) })
		for _, want := range []string{"group/mb-perf: magpie serves none of its models", "Copilot is off now, skipped"} {
			if !strings.Contains(out, want) {
				t.Fatalf("status lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("a group magpie really lacks is still missing", func(t *testing.T) {
		f := withFakeMagpie(t)
		f.groups["mb-fast"], f.groups["mb-balanced"] = []string{"a/x"}, []string{"a/y"}
		f.order = []string{"mb-fast", "mb-balanced"}
		writeRouterJSON(t, "127.0.0.1:1")
		out, _ := captureStdout(t, func() error { return routerCheck(nil) })
		if !strings.Contains(out, "performance group missing in magpie: group/mb-perf") ||
			!strings.Contains(out, "mbridge router init --groups-only --force") {
			t.Fatalf("check:\n%s", out)
		}
	})
}
