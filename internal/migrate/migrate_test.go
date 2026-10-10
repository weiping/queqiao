package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// home lays out a qq-v0.1.4 install (testdata) under a fresh HOME and
// returns the Env that migrates it, with the old gateway down and magpie
// installed.
func home(t *testing.T) Env {
	t.Helper()
	h := t.TempDir()
	layTree(t, "testdata/qq-v0.1.4-layout/config", filepath.Join(h, ".config"))
	layTree(t, "testdata/qq-v0.1.4-layout/cache", filepath.Join(h, ".cache"))
	return Env{
		Home: h, GOOS: "linux",
		ConfigHome: filepath.Join(h, ".config"), CacheHome: filepath.Join(h, ".cache"),
		SystemCache:       filepath.Join(h, "syscache"),
		Now:               time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC),
		OldGatewayRunning: func(context.Context) bool { return false },
		MagpieVersion:     func(context.Context) (string, error) { return "magpie v0.1.1000", nil },
	}
}

func layTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		st, _ := d.Info()
		return os.WriteFile(dst, b, st.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// snapshot is every file under dirs: relative path → mode and content hash.
func snapshot(t *testing.T, root string, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, d := range dirs {
		filepath.WalkDir(filepath.Join(root, d), func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			b, _ := os.ReadFile(p)
			st, _ := e.Info()
			sum := sha256.Sum256(b)
			rel, _ := filepath.Rel(root, p)
			out[rel] = st.Mode().Perm().String() + " " + hex.EncodeToString(sum[:8])
			return nil
		})
	}
	return out
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func TestMigrateMovesMagpieFilesKeepsQueqiaos(t *testing.T) {
	env := home(t)
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	q, m := filepath.Join(env.ConfigHome, "queqiao"), filepath.Join(env.ConfigHome, "magpie")
	for _, f := range []string{"providers.json", "usage.jsonl", "settings.json", "stash.json", "applied.json", "cli-identity.json", "migrations.json", "plugins.json"} {
		if !exists(filepath.Join(m, f)) {
			t.Errorf("magpie lacks %s", f)
		}
		if exists(filepath.Join(q, f)) {
			t.Errorf("queqiao still holds magpie's %s", f)
		}
	}
	for _, f := range []string{"router.json", "router.jsonl", "logs/queqiaod.log"} {
		if !exists(filepath.Join(q, f)) {
			t.Errorf("queqiao lost its %s", f)
		}
	}
	if !exists(filepath.Join(env.CacheHome, "magpie", "models.json")) || exists(filepath.Join(env.CacheHome, "queqiao")) {
		t.Error("cache not handed to magpie")
	}
	// the providers file keeps its 0600
	st, err := os.Stat(filepath.Join(m, "providers.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("providers.json mode %v, %v", st.Mode().Perm(), err)
	}
	// and a full copy of the old config sits in the backup
	if !exists(filepath.Join(plan.Backup, "queqiao-config", "providers.json")) {
		t.Error("backup lacks the old config")
	}
	if !strings.HasPrefix(filepath.Base(plan.Backup), "sp8-20261010-093000") {
		t.Errorf("backup dir %s", plan.Backup)
	}
}

func TestMigrateWithExistingMagpieDirRoundTrips(t *testing.T) {
	env := home(t)
	m := filepath.Join(env.ConfigHome, "magpie")
	os.MkdirAll(m, 0o755)
	os.WriteFile(filepath.Join(m, "providers.json"), []byte(`{"old":"magpie"}`), 0o640)
	before := snapshot(t, env.Home, ".config", ".cache", "syscache")

	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(plan.Backup, "magpie-before", "providers.json")); string(b) != `{"old":"magpie"}` {
		t.Fatalf("magpie-before holds %q", b)
	}
	// magpie runs on in between and writes a file of its own
	os.WriteFile(filepath.Join(m, "new-after.json"), []byte("{}"), 0o644)

	if err := Restore(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, env.Home, ".config", ".cache", "syscache")
	for _, k := range keys(before) {
		if after[k] != before[k] {
			t.Errorf("%s: before %q, after %q", k, before[k], after[k])
		}
	}
	for _, k := range keys(after) {
		if _, ok := before[k]; !ok && !strings.HasPrefix(k, filepath.Join(".config", "queqiao-migration")) {
			t.Errorf("restore left %s", k)
		}
	}
	// what magpie wrote after the migration is kept, in the backup
	found := false
	filepath.WalkDir(plan.Backup, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "new-after.json" {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("magpie's post-migration file was not kept")
	}
}

func TestMigrateUnknownFileGoesToMagpie(t *testing.T) {
	env := home(t)
	os.WriteFile(filepath.Join(env.ConfigHome, "queqiao", "something-new.json"), []byte("{}"), 0o644)
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(env.ConfigHome, "magpie", "something-new.json")) {
		t.Fatal("a file off the queqiao list stayed behind")
	}
}

func TestMigrateDryRunChangesNothing(t *testing.T) {
	env := home(t)
	before := snapshot(t, env.Home, ".config", ".cache", "syscache")
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) == 0 || plan.String() == "" {
		t.Fatal("a plan with nothing to say")
	}
	after := snapshot(t, env.Home, ".config", ".cache", "syscache")
	if len(after) != len(before) {
		t.Fatalf("preparing changed files: %d → %d", len(before), len(after))
	}
	if exists(filepath.Join(env.ConfigHome, "queqiao-migration")) {
		t.Fatal("preparing made a backup folder")
	}
}

func TestMigrateRefusesWhileOldGatewayRuns(t *testing.T) {
	env := home(t)
	env.OldGatewayRunning = func(context.Context) bool { return true }
	_, err := Prepare(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), "3425") {
		t.Fatalf("err %v", err)
	}
}

func TestMigrateNeedsMagpie(t *testing.T) {
	env := home(t)
	env.MagpieVersion = func(context.Context) (string, error) { return "", errors.New("not found") }
	if _, err := Prepare(context.Background(), env); err == nil || !strings.Contains(err.Error(), "magpie") {
		t.Fatalf("err %v", err)
	}
}

func TestMigrateCleansLegacyCodexTables(t *testing.T) {
	env := home(t)
	codex := filepath.Join(env.Home, ".codex")
	os.MkdirAll(codex, 0o755)
	cfg := "model = \"group/queqiao\"\nmodel_provider = \"magpie\"\n\n[model_providers.magpie]\nname = \"magpie\"\n"
	os.WriteFile(filepath.Join(codex, "config.toml"), []byte(cfg), 0o644)
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(codex, "config.toml"))
	if strings.Contains(string(b), "group/queqiao") {
		t.Fatalf("legacy top-level model kept:\n%s", b)
	}
	if !exists(filepath.Join(codex, "queqiao.config.toml")) {
		t.Fatal("no queqiao profile written")
	}
	if b, _ := os.ReadFile(filepath.Join(plan.Backup, "codex-config.toml")); string(b) != cfg {
		t.Fatal("Codex config not backed up as it was")
	}
	if err := Restore(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(codex, "config.toml")); string(b) != cfg {
		t.Fatalf("Codex config not restored:\n%s", b)
	}
}

// The old app's own login item launched the queqiao binary under magpie's
// autostart name; it moves to the backup, magpie's own does not.
func TestMigrateMovesOldAutostartOnly(t *testing.T) {
	env := home(t)
	auto := filepath.Join(env.ConfigHome, "autostart")
	os.MkdirAll(auto, 0o755)
	os.WriteFile(filepath.Join(auto, "magpie.desktop"), []byte("[Desktop Entry]\nExec=/home/u/.local/bin/queqiao tray\n"), 0o644)
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(auto, "magpie.desktop")) {
		t.Fatal("the old app's autostart entry stayed")
	}

	env2 := home(t)
	auto2 := filepath.Join(env2.ConfigHome, "autostart")
	os.MkdirAll(auto2, 0o755)
	os.WriteFile(filepath.Join(auto2, "magpie.desktop"), []byte("[Desktop Entry]\nExec=/usr/bin/magpie tray\n"), 0o644)
	plan2, _ := Prepare(context.Background(), env2)
	plan2.Apply(context.Background())
	if !exists(filepath.Join(auto2, "magpie.desktop")) {
		t.Fatal("magpie's own autostart entry was moved")
	}
}

// Migration and restore only move and copy: every file there was before is
// still somewhere under HOME, with its content, after Apply and after
// Restore.
func TestMigrateNeverDeletes(t *testing.T) {
	env := home(t)
	os.MkdirAll(filepath.Join(env.ConfigHome, "magpie"), 0o755)
	os.WriteFile(filepath.Join(env.ConfigHome, "magpie", "mine.json"), []byte(`{"m":1}`), 0o644)
	contents := func() map[string]int {
		out := map[string]int{}
		filepath.WalkDir(env.Home, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				out[string(b)]++
			}
			return nil
		})
		return out
	}
	before := contents()
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterApply := contents()
	if err := Restore(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	afterRestore := contents()
	for c, n := range before {
		if afterApply[c] < n || afterRestore[c] < n {
			t.Errorf("content %.40q: %d before, %d after apply, %d after restore", c, n, afterApply[c], afterRestore[c])
		}
	}
}

func TestRestoreWithoutBackupSaysSo(t *testing.T) {
	env := home(t)
	if err := Restore(context.Background(), env); err == nil || !strings.Contains(err.Error(), "no SP8 migration backup") {
		t.Fatalf("err %v", err)
	}
}

// On Linux the system cache folder is ~/.cache itself: it is handed over
// once, not twice.
func TestMigrateSameCacheFolderOnce(t *testing.T) {
	env := home(t)
	env.SystemCache = env.CacheHome
	plan, err := Prepare(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
}
