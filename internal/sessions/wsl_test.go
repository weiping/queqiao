package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wslSettle waits for the listing of the WSL distros under way, if any.
func wslSettle() {
	wslSess.Lock()
	done := wslSess.done
	wslSess.Unlock()
	if done != nil {
		<-done
	}
}

// wslDistro is a distro's home in a temp dir with Claude Code's, Codex's
// and Pi's sessions in it, and this computer's own folders empty; WSLHomes
// says it runs while *running is true.
func wslDistro(t *testing.T) (home string, running *bool) {
	setup(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "own-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, "own-codex"))
	home = filepath.Join(dir, "Ubuntu", "home", "me")
	copyTree(t, "testdata/claude", filepath.Join(home, ".claude"))
	copyTree(t, "testdata/codex", filepath.Join(home, ".codex"))
	copyTree(t, "testdata/pi", filepath.Join(home, ".pi", "agent"))
	on := true
	WSLHomes = func() []WSLHome { return []WSLHome{{Distro: "Ubuntu", Home: home, Running: on}} }
	t.Cleanup(func() { wslSettle(); WSLHomes = nil; Reset() })
	Reset()
	return home, &on
}

// The sessions of Claude Code, Codex and Pi in a WSL distro are listed with
// the distro, resumed there through wsl.exe; their calls count in usage.
func TestWSLSessionsListed(t *testing.T) {
	home, _ := wslDistro(t)
	ss := List(0)
	got := map[string]Session{}
	for _, s := range ss {
		if s.WSL != "Ubuntu" || !strings.HasPrefix(s.Path, home) {
			t.Fatalf("a session not from the distro: %+v", s)
		}
		got[s.Agent] = s
	}
	for _, a := range []string{"claude", "codex", "pi"} {
		if _, ok := got[a]; !ok {
			t.Fatalf("no %s session from WSL in %+v", a, ss)
		}
	}
	cc := got["claude"]
	if cc.ID != "11111111-2222-3333-4444-555555555555" || cc.Cwd != "/work/app" || cc.Tokens.zero() {
		t.Fatalf("claude: %+v", cc)
	}
	want := `wsl.exe -d 'Ubuntu' --cd '/work/app' -e sh -lc 'exec ${SHELL:-sh} -lic ''claude --resume 11111111-2222-3333-4444-555555555555'''`
	if cc.Resume != want {
		t.Fatalf("resume\n got %s\nwant %s", cc.Resume, want)
	}
	if r := got["codex"].Resume; !strings.HasPrefix(r, "wsl.exe -d 'Ubuntu' ") || !strings.Contains(r, "''codex resume ") {
		t.Fatalf("codex resume %q", r)
	}
	var calls int
	for _, c := range Calls(time.Time{}) {
		if c.Agent == "claude" || c.Agent == "codex" {
			calls++
		}
	}
	if calls == 0 {
		t.Fatal("the distro's calls aren't counted")
	}
	dirs := strings.Join(Dirs(), "\n")
	if !strings.Contains(dirs, filepath.Join(home, ".claude")) || !strings.Contains(dirs, filepath.Join(home, ".codex")) {
		t.Fatalf("dirs %s", dirs)
	}
}

// A session in a WSL distro is deleted as this computer's are (TJHHHH: 请问
// 是否可以增加wsl内对于会话的删除呢): moved to magpie's trash, its Claude Code
// files from the distro's ~/.claude and not this computer's, gone from the
// listing at once; restored, it is back in the distro and listed again.
func TestWSLSessionDeleted(t *testing.T) {
	home, _ := wslDistro(t)
	const id = "11111111-2222-3333-4444-555555555555"
	own := os.Getenv("CLAUDE_CONFIG_DIR")
	distro := filepath.Join(home, ".claude")
	for _, p := range []string{filepath.Join(distro, "file-history", id, "a@v1"), filepath.Join(own, "file-history", id, "a@v1")} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	old := time.Now().Add(-time.Hour)
	filepath.WalkDir(home, func(p string, _ os.DirEntry, _ error) error { return os.Chtimes(p, old, old) })
	List(0)
	wslSettle()
	m, ok := findManaged(ListAgent("claude"), id)
	if !ok || m.WSL != "Ubuntu" || !m.Deletable {
		t.Fatalf("the WSL session listed as %+v, %v", m, ok)
	}
	tr, err := Delete("claude", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.Path, filepath.Join(distro, "file-history", id)} {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("%s is still there", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(own, "file-history", id, "a@v1")); err != nil {
		t.Fatal("this computer's own file history was moved")
	}
	if _, ok := findManaged(ListAgent("claude"), id); ok {
		t.Fatal("still listed")
	}
	if trash := Trash(); len(trash) != 1 || trash[0].Key != tr.Key {
		t.Fatalf("trash %+v", trash)
	}
	if _, err := Restore(tr.Key); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.Path, filepath.Join(distro, "file-history", id, "a@v1")} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("%s wasn't restored", p)
		}
	}
	if m, ok := findManaged(ListAgent("claude"), id); !ok || m.WSL != "Ubuntu" {
		t.Fatalf("restored session listed as %+v, %v", m, ok)
	}
}

// A stopped distro's sessions are listed as last read, and nothing in it is
// opened: that would start it.
func TestWSLStoppedDistroKept(t *testing.T) {
	home, running := wslDistro(t)
	if n := len(List(0)); n != 4 {
		t.Fatalf("want the 4 sessions, got %d", n)
	}
	wslSettle()
	Saved()
	// magpie starts again; the distro has stopped, and its files can't be
	// read (a read would start it): here they are gone
	*running = false
	Reset()
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		ss := List(0)
		if len(ss) != 4 {
			t.Fatalf("pass %d: want the 4 sessions as last read, got %+v", i, ss)
		}
		for _, s := range ss {
			if s.WSL != "Ubuntu" || s.Tokens.zero() || s.Resume == "" {
				t.Fatalf("pass %d: %+v", i, s)
			}
		}
		wslSettle()
	}
}

// Off WSL (WSLHomes nil, as on macOS and Linux) nothing changes.
func TestWSLNoneWithoutHomes(t *testing.T) {
	setup(t)
	if WSLHomes != nil {
		t.Fatal("WSLHomes set in this package's tests")
	}
	for _, s := range List(0) {
		if s.WSL != "" {
			t.Fatalf("%+v", s)
		}
	}
	if r := ResumeCommand("claude", "abc", ""); r != "claude --resume abc" {
		t.Fatal(r)
	}
}
