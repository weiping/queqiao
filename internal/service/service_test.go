package service

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden unit files")

// Each platform's unit is checked against a golden file, and against the
// spec's fixed values (§5.7) so a regenerated golden can't drift from them.
func TestUnitContent(t *testing.T) {
	cases := []struct {
		goos, golden string
		path         string
		must         []string
	}{
		{"darwin", "launchd.plist.golden", "/home/u/Library/LaunchAgents/io.github.weiping.queqiao.plist",
			[]string{"<string>io.github.weiping.queqiao</string>", "<key>RunAtLoad</key>", "<key>KeepAlive</key>", "<string>/opt/q/queqiao</string>", "<string>serve</string>"}},
		{"linux", "queqiao.service.golden", "/home/u/.config/systemd/user/queqiao.service",
			[]string{"ExecStart=/opt/q/queqiao serve", "Restart=on-failure", "WantedBy=default.target"}},
		{"windows", "task.xml.golden", "/home/u/.config/queqiao/queqiao-task.xml",
			[]string{"<LogonTrigger>", "<Command>/opt/q/queqiao</Command>", "<Arguments>serve</Arguments>"}},
	}
	for _, c := range cases {
		path, content := Unit(c.goos, "/home/u", "/home/u/.config/queqiao", "/opt/q/queqiao")
		if filepath.ToSlash(path) != c.path {
			t.Errorf("%s: path %s, want %s", c.goos, path, c.path)
		}
		for _, m := range c.must {
			if !strings.Contains(string(content), m) {
				t.Errorf("%s: unit lacks %q", c.goos, m)
			}
		}
		g := filepath.Join("testdata", c.golden)
		if *update {
			os.WriteFile(g, content, 0o644)
		}
		want, err := os.ReadFile(g)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != string(content) {
			t.Errorf("%s: unit differs from %s:\n%s", c.goos, g, content)
		}
	}
}

// Nine days of logging leave seven files: today's queqiaod.log and the six
// days before it.
func TestDailyLogKeepsSeven(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	l := newDailyLog(dir, 7, func() time.Time { return day })
	for i := 0; i < 9; i++ {
		if _, err := l.Write([]byte("line\n")); err != nil {
			t.Fatal(err)
		}
		day = day.Add(24 * time.Hour)
	}
	l.Close()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 7 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("%d files, want 7: %v", len(ents), names)
	}
	if _, err := os.Stat(filepath.Join(dir, "queqiaod.log")); err != nil {
		t.Fatal("no current queqiaod.log:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "queqiaod-2026-10-02.log")); err == nil {
		t.Fatal("the oldest day was kept")
	}
}

// Installing twice writes the same unit and asks the service manager the
// same things; nothing errors the second time.
func TestInstallTwiceIsIdempotent(t *testing.T) {
	home := t.TempDir()
	var calls []string
	m := &Manager{GOOS: "linux", Home: home, ConfigDir: filepath.Join(home, ".config", "queqiao"),
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return nil, nil
		}}
	for i := 0; i < 2; i++ {
		if err := m.Install(context.Background(), "/opt/q/queqiao"); err != nil {
			t.Fatal(err)
		}
	}
	path, want := Unit("linux", home, m.ConfigDir, "/opt/q/queqiao")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("unit not written as Unit says: %v", err)
	}
	if len(calls) != 4 || calls[0] != calls[2] || calls[1] != calls[3] {
		t.Fatalf("calls %v", calls)
	}
	if calls[1] != "systemctl --user enable --now queqiao.service" {
		t.Fatalf("enable call %q", calls[1])
	}
}

// Uninstall removes the unit after stopping it; uninstalling what isn't
// installed is not an error.
func TestUninstallWhenNotInstalled(t *testing.T) {
	home := t.TempDir()
	m := &Manager{GOOS: "darwin", Home: home, ConfigDir: filepath.Join(home, ".config", "queqiao"),
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) { return nil, nil }}
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
}
