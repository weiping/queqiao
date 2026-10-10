package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetJSONKeepsOtherKeysAndOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(`{"a":1,"env":{"X":"y"},"z":[1,2]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetJSON(p, KV{Path: "env.ANTHROPIC_MODEL", Value: "group/queqiao"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "{\n  \"a\": 1,\n  \"env\": {\n    \"X\": \"y\",\n    \"ANTHROPIC_MODEL\": \"group/queqiao\"\n  },\n  \"z\": [\n    1,\n    2\n  ]\n}\n"
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
}

func TestSetJSONReplacesExistingValueInPlace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"env":{"A":"1","B":"2"}}`), 0o644)
	if err := SetJSON(p, KV{Path: "env.A", Value: "3"}); err != nil {
		t.Fatal(err)
	}
	raw, ok, err := GetJSON(p, "env.A")
	if err != nil || !ok || string(raw) != `"3"` {
		t.Fatalf("env.A = %s %v %v", raw, ok, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "{\n  \"env\": {\n    \"A\": \"3\",\n    \"B\": \"2\"\n  }\n}\n" {
		t.Fatalf("order changed: %s", b)
	}
}

func TestSetJSONOnMissingFileCreatesIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "new.json")
	if err := SetJSON(p, KV{Path: "a.b", Value: true}); err != nil {
		t.Fatal(err)
	}
	raw, ok, err := GetJSON(p, "a.b")
	if err != nil || !ok || string(raw) != "true" {
		t.Fatalf("a.b = %s %v %v", raw, ok, err)
	}
}

func TestUnreadableJSONIsAnErrorNotEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte(`{bad`), 0o644)
	if err := SetJSON(p, KV{Path: "a", Value: 1}); err == nil {
		t.Fatal("SetJSON on unparseable file succeeded")
	}
	if b, _ := os.ReadFile(p); string(b) != `{bad` {
		t.Fatalf("file changed: %q", b)
	}
	if _, _, err := GetJSON(p, "a"); err == nil {
		t.Fatal("GetJSON on unparseable file gave no error")
	}
	if err := DelJSON(p, "a"); err == nil {
		t.Fatal("DelJSON on unparseable file gave no error")
	}
}

func TestDelJSONRemovesOnlyThatKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	os.WriteFile(p, []byte(`{"magpie":{"default":"x"},"keep":1}`), 0o644)
	if err := DelJSON(p, "magpie"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := GetJSON(p, "magpie"); ok {
		t.Fatal("magpie still there")
	}
	if raw, ok, _ := GetJSON(p, "keep"); !ok || string(raw) != "1" {
		t.Fatal("keep lost")
	}
}

func TestWriteAtomicKeepsMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("old"), 0o600)
	if err := WriteAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	// Windows has no permission bits to keep (only read-only)
	if st, _ := os.Stat(p); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("content %q", b)
	}
}

func TestConfigDirHonoursXDGAndOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("QUEQIAO_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := ConfigDir(), filepath.Join(home, ".config", "queqiao"); got != want {
		t.Fatalf("default %q want %q", got, want)
	}
	x := filepath.Join(home, "x")
	t.Setenv("XDG_CONFIG_HOME", x)
	if got := ConfigDir(); got != filepath.Join(x, "queqiao") {
		t.Fatalf("xdg %q", got)
	}
	t.Setenv("QUEQIAO_CONFIG_DIR", "/o")
	if got := ConfigDir(); got != "/o" {
		t.Fatalf("override %q", got)
	}
}
