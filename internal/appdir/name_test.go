package appdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetName starts from the default (name "magpie", not yet handed out) and
// restores that state after the test, so no test leaks its rename into the
// next one.
func resetName(t *testing.T) {
	t.Helper()
	nameMu.Lock()
	name, nameFixed = "magpie", false
	nameMu.Unlock()
	t.Cleanup(func() {
		nameMu.Lock()
		name, nameFixed = "magpie", false
		nameMu.Unlock()
	})
}

func TestSetNameRenamesFolders(t *testing.T) {
	resetName(t)
	UseExecutable("")
	x, c := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	t.Setenv("XDG_CACHE_HOME", c)
	SetName("queqiao")
	if got := Config(); got != filepath.Join(x, "queqiao") {
		t.Fatalf("Config() = %q, want %q", got, filepath.Join(x, "queqiao"))
	}
	if got := Cache(); got != filepath.Join(c, "queqiao") {
		t.Fatalf("Cache() = %q, want %q", got, filepath.Join(c, "queqiao"))
	}
	if d, _ := SystemCache(); filepath.Base(d) != "queqiao" {
		t.Fatalf("SystemCache() = %q, want base queqiao", d)
	}
}

func TestSetNameDefaultsToMagpie(t *testing.T) {
	resetName(t)
	UseExecutable("")
	x, c := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	t.Setenv("XDG_CACHE_HOME", c)
	if got := Config(); filepath.Base(got) != "magpie" {
		t.Fatalf("Config() = %q, want base magpie", got)
	}
	if got := Cache(); filepath.Base(got) != "magpie" {
		t.Fatalf("Cache() = %q, want base magpie", got)
	}
}

func TestSetNameAfterUseRefuses(t *testing.T) {
	resetName(t)
	UseExecutable("")
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	_ = Config()
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), `SetName("queqiao")`) {
			t.Fatalf("recovered %v, want a panic naming SetName", r)
		}
	}()
	SetName("queqiao")
}

func TestSetNameKeepsPortable(t *testing.T) {
	resetName(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	UseExecutable(filepath.Join(dir, "magpie"))
	SetName("queqiao")
	if got := Config(); filepath.Base(got) != "data" {
		t.Fatalf("Config() = %q, want the portable data folder", got)
	}
}
