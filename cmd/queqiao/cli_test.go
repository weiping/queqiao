package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/weiping/queqiao/internal/migrate"
	"github.com/weiping/queqiao/internal/service"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/weiping/queqiao/internal/fsutil"
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

func TestCLIListsOnlyQueqiaoCommands(t *testing.T) {
	for _, want := range []string{"queqiao serve", "queqiao service", "queqiao status", "queqiao migrate",
		"queqiao router init", "queqiao router report", "queqiao router calibrate", "queqiao hook", "queqiao update", "queqiao version"} {
		if !strings.Contains(usageText, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, gone := range []string{"queqiao provider", "queqiao group ", "queqiao tui", "queqiao web", "queqiao accounts"} {
		if strings.Contains(usageText, gone) {
			t.Errorf("help still has magpie's %q", gone)
		}
	}
	if code := run([]string{"provider", "add"}); code == 0 {
		t.Fatal("a magpie command was accepted")
	}
}

// writeRouterJSON writes a valid router.json with queqiaod at listen.
func writeRouterJSON(t *testing.T, listen string) {
	t.Helper()
	body := fmt.Sprintf(`{"version":1,"router_group":"queqiao","tiers":{"fast":{"group":"qq-fast","claude_alias":"haiku"},"balanced":{"group":"qq-balanced","claude_alias":"sonnet"},"performance":{"group":"qq-perf","claude_alias":"opus"}},"listen":%q}`, listen)
	os.MkdirAll(fsutil.ConfigDir(), 0o755)
	if err := os.WriteFile(routerJSONPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeQueqiaod(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/queqiao/router" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"valid": true, "decisions": []map[string]any{
			{"session": "s-1", "tier": "fast", "reason": "R5-classified"}}})
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestStatusReportsMagpieAndQueqiaod(t *testing.T) {
	t.Run("all up, a group missing", func(t *testing.T) {
		f := withFakeMagpie(t)
		f.groups["qq-fast"], f.groups["qq-balanced"] = []string{"a/x"}, []string{"a/y"}
		f.order = []string{"qq-fast", "qq-balanced"}
		writeRouterJSON(t, fakeQueqiaod(t))
		out, err := captureStdout(t, func() error { return statusCmd(nil) })
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"magpie v0.1.1100", "queqiaod", "group/qq-perf missing", "s-1 → fast (R5-classified)"} {
			if !strings.Contains(out, want) {
				t.Fatalf("status lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("magpie down", func(t *testing.T) {
		f := withFakeMagpie(t)
		f.down = true
		writeRouterJSON(t, fakeQueqiaod(t))
		out, _ := captureStdout(t, func() error { return statusCmd(nil) })
		if !strings.Contains(out, "magpie isn't answering") {
			t.Fatalf("status:\n%s", out)
		}
	})
	t.Run("queqiaod down", func(t *testing.T) {
		withFakeMagpie(t)
		writeRouterJSON(t, "127.0.0.1:1")
		out, _ := captureStdout(t, func() error { return statusCmd(nil) })
		if !strings.Contains(out, "queqiaod isn't running on 127.0.0.1:1") {
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
	if !strings.Contains(out, "queqiao "+version) || !strings.Contains(out, "magpie v0.1.1100") {
		t.Fatalf("version:\n%s", out)
	}
}

// fakeReleases serves a GitHub releases list with one qq-v release whose
// asset for this platform is body, and checksums.txt saying sum.
func fakeReleases(t *testing.T, body, sum string) string {
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/weiping/queqiao/releases":
			json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": "v9.9.9", "assets": []any{}},
				{"tag_name": "qq-v0.2.0", "assets": []map[string]any{
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
	exe := filepath.Join(t.TempDir(), "queqiao")
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
	b, _ := os.ReadFile(exe)
	st, _ := os.Stat(exe)
	if string(b) != "new binary" || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("%q %v", b, st.Mode())
	}
}

// `queqiao service status` says what the service manager says, and an
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

// --dry-run prints the plan and changes nothing; restore without a backup
// says there is none.
func TestMigrateDryRunAndRestoreCLI(t *testing.T) {
	h := t.TempDir()
	os.MkdirAll(filepath.Join(h, ".config", "queqiao"), 0o755)
	os.WriteFile(filepath.Join(h, ".config", "queqiao", "providers.json"), []byte("{}"), 0o600)
	old := migrateEnv
	migrateEnv = func() migrate.Env {
		return migrate.Env{Home: h, GOOS: "linux", ConfigHome: filepath.Join(h, ".config"), CacheHome: filepath.Join(h, ".cache"),
			SystemCache: filepath.Join(h, ".cache"), Now: time.Unix(0, 0),
			OldGatewayRunning: func(context.Context) bool { return false },
			MagpieVersion:     func(context.Context) (string, error) { return "v", nil }}
	}
	t.Cleanup(func() { migrateEnv = old })
	out, err := captureStdout(t, func() error { return migrateCmd([]string{"--dry-run"}) })
	if err != nil || !strings.Contains(out, "providers.json") || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(h, ".config", "magpie")); err == nil {
		t.Fatal("dry run made magpie's folder")
	}
	if _, err := captureStdout(t, func() error { return migrateCmd([]string{"restore"}) }); err == nil {
		t.Fatal("restore with no backup succeeded")
	}
}
