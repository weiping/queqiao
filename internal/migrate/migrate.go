// Package migrate hands a qq-v0.1.x install's magpie data back to official
// magpie (SP8 §7.1). qq-v0.1.x was magpie renamed: it kept magpie's files in
// ~/.config/queqiao and ~/.cache/queqiao. Official magpie reads
// ~/.config/magpie. Migration moves magpie's files there, keeps queqiao's
// own (QueqiaoFiles) where they are, and records every move in a backup
// folder, from which Restore puts everything back. Nothing is removed:
// files are moved or copied, never deleted.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weiping/queqiao/internal/codexcfg"
	"github.com/weiping/queqiao/internal/fsutil"
)

// QueqiaoFiles are the entries of ~/.config/queqiao that are queqiao's
// own: they stay. Everything else there is magpie's.
var QueqiaoFiles = []string{"router.json", "router.jsonl", "logs"}

// Env is where an install lives and how to ask about the running world.
type Env struct {
	Home        string
	GOOS        string
	ConfigHome  string // $XDG_CONFIG_HOME, else ~/.config
	CacheHome   string // $XDG_CACHE_HOME, else ~/.cache
	SystemCache string // os.UserCacheDir()
	Now         time.Time
	// OldGatewayRunning: a qq-v0.1.x gateway answers on 127.0.0.1:3425
	OldGatewayRunning func(context.Context) bool
	MagpieVersion     func(context.Context) (string, error)
	QueqiaoURL        string // what the Codex profile points at; default http://127.0.0.1:3426
}

// Step is one thing Apply does. Op is "copy" (From copied to To), "move"
// (From renamed to To), "mkdir" (To made), "codex" (Codex's config backed
// up to To, then cleaned and the queqiao profile written) or "note"
// (nothing done; Note told).
type Step struct {
	Op   string `json:"op"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Note string `json:"note,omitempty"`
}

// Plan is what Apply will do, and where the backup goes.
type Plan struct {
	Steps  []Step
	Backup string
	env    Env
}

func (e Env) queqiao() string     { return filepath.Join(e.ConfigHome, "queqiao") }
func (e Env) magpie() string      { return filepath.Join(e.ConfigHome, "magpie") }
func (e Env) backupRoot() string  { return filepath.Join(e.ConfigHome, "queqiao-migration") }
func (e Env) codexHome() string   { return filepath.Join(e.Home, ".codex") }
func exists(p string) bool        { _, err := os.Lstat(p); return err == nil }
func isQueqiaos(name string) bool { return contains(QueqiaoFiles, name) }

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// Prepare checks the install and works out the steps; it changes nothing.
func Prepare(ctx context.Context, env Env) (Plan, error) {
	if env.MagpieVersion != nil {
		if _, err := env.MagpieVersion(ctx); err != nil {
			return Plan{}, fmt.Errorf("official magpie is not installed (magpie version: %v); install it first: https://github.com/yetone/magpie", err)
		}
	}
	if env.OldGatewayRunning != nil && env.OldGatewayRunning(ctx) {
		return Plan{}, fmt.Errorf("the old queqiao app or gateway still answers on 127.0.0.1:3425; quit it (and its menu bar icon), then migrate again")
	}
	if env.QueqiaoURL == "" {
		env.QueqiaoURL = "http://127.0.0.1:3426"
	}
	q := env.queqiao()
	ents, err := os.ReadDir(q)
	if err != nil {
		return Plan{}, fmt.Errorf("nothing to migrate: %v", err)
	}
	b := filepath.Join(env.backupRoot(), "sp8-"+env.Now.Format("20060102-150405"))
	p := Plan{Backup: b, env: env}
	add := func(s Step) { p.Steps = append(p.Steps, s) }

	// 1) the whole of it, as it is, into the backup
	add(Step{Op: "copy", From: q, To: filepath.Join(b, "queqiao-config")})
	caches := []struct{ from, to, name string }{
		{filepath.Join(env.CacheHome, "queqiao"), filepath.Join(env.CacheHome, "magpie"), "cache"},
		{filepath.Join(env.SystemCache, "queqiao"), filepath.Join(env.SystemCache, "magpie"), "system-cache"},
	}
	if filepath.Clean(env.SystemCache) == filepath.Clean(env.CacheHome) {
		caches = caches[:1] // Linux: os.UserCacheDir is ~/.cache too
	}
	for _, c := range caches {
		if exists(c.from) {
			add(Step{Op: "copy", From: c.from, To: filepath.Join(b, "queqiao-"+c.name)})
		}
	}
	// 2) a magpie folder already there goes into the backup whole
	m := env.magpie()
	if exists(m) {
		add(Step{Op: "move", From: m, To: filepath.Join(b, "magpie-before")})
	}
	add(Step{Op: "mkdir", To: m})
	// 3) magpie's files from queqiao's folder to magpie's
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	for _, e := range ents {
		if isQueqiaos(e.Name()) {
			continue
		}
		add(Step{Op: "move", From: filepath.Join(q, e.Name()), To: filepath.Join(m, e.Name())})
	}
	for _, c := range caches {
		if !exists(c.from) {
			continue
		}
		if exists(c.to) {
			add(Step{Op: "move", From: c.to, To: filepath.Join(b, "magpie-"+c.name+"-before")})
		}
		add(Step{Op: "move", From: c.from, To: c.to})
	}
	// 4) the old app and its login item
	for _, s := range oldApp(env) {
		add(Step{Op: "move", From: s, To: filepath.Join(b, "removed", strings.TrimPrefix(filepath.ToSlash(s), "/"))})
	}
	if env.GOOS == "windows" {
		add(Step{Op: "note", Note: `the old app's login item is the "magpie" value under HKCU\Software\Microsoft\Windows\CurrentVersion\Run; run "magpie autostart on" to point it at official magpie`})
	}
	// 5) Codex: what qq-v0.1.x wrote into config.toml out, the profile in
	add(Step{Op: "codex", From: filepath.Join(env.codexHome(), "config.toml"), To: filepath.Join(b, "codex-config.toml")})
	return p, nil
}

// oldApp lists the qq-v0.1.x app and its login items: the app bundle on a
// Mac, and magpie's autostart entries when they start a queqiao binary
// (the fork kept magpie's autostart names).
func oldApp(env Env) []string {
	var out []string
	switch env.GOOS {
	case "darwin":
		for _, d := range []string{"/Applications", filepath.Join(env.Home, "Applications")} {
			for _, n := range []string{"Queqiao.app", "queqiao.app"} {
				if p := filepath.Join(d, n); exists(p) {
					out = append(out, p)
				}
			}
		}
		if p := filepath.Join(env.Home, "Library", "LaunchAgents", "com.yetone.magpie.plist"); startsQueqiao(p) {
			out = append(out, p)
		}
	case "linux":
		if p := filepath.Join(env.ConfigHome, "autostart", "magpie.desktop"); startsQueqiao(p) {
			out = append(out, p)
		}
	}
	return out
}

func startsQueqiao(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), "queqiao")
}

// String is the plan in words, for --dry-run and the confirmation.
func (p Plan) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "backup: %s\n", p.Backup)
	for _, s := range p.Steps {
		switch s.Op {
		case "copy":
			fmt.Fprintf(&b, "  copy   %s → %s\n", s.From, s.To)
		case "move":
			fmt.Fprintf(&b, "  move   %s → %s\n", s.From, s.To)
		case "mkdir":
			fmt.Fprintf(&b, "  make   %s\n", s.To)
		case "codex":
			fmt.Fprintf(&b, "  codex  back up %s, take out qq-v0.1.x's entries, write queqiao.config.toml\n", s.From)
		case "note":
			fmt.Fprintf(&b, "  note   %s\n", s.Note)
		}
	}
	return b.String()
}

// manifest is what Apply did, kept in the backup for Restore.
type manifest struct {
	Done []Step `json:"done"`
}

// Apply runs the steps, recording each one done.
func (p Plan) Apply(ctx context.Context) error {
	if err := os.MkdirAll(p.Backup, 0o700); err != nil {
		return err
	}
	var man manifest
	for _, s := range p.Steps {
		if err := p.do(s); err != nil {
			return fmt.Errorf("%s %s: %v (done so far is in %s; queqiao migrate restore undoes it)", s.Op, s.From, err, p.Backup)
		}
		man.Done = append(man.Done, s)
		b, _ := json.MarshalIndent(man, "", "  ")
		if err := fsutil.WriteAtomic(filepath.Join(p.Backup, "manifest.json"), b); err != nil {
			return err
		}
	}
	return nil
}

func (p Plan) do(s Step) error {
	switch s.Op {
	case "copy":
		return copyTree(s.From, s.To)
	case "move":
		return move(s.From, s.To)
	case "mkdir":
		return os.MkdirAll(s.To, 0o700)
	case "codex":
		if b, err := os.ReadFile(s.From); err == nil {
			if err := os.WriteFile(s.To, b, 0o600); err != nil {
				return err
			}
			if _, err := codexcfg.CleanLegacy(filepath.Dir(s.From)); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		_, err := codexcfg.Init(filepath.Dir(s.From), p.env.QueqiaoURL)
		return err
	}
	return nil
}

// Restore undoes the latest migration from its backup, newest step first.
// What magpie wrote in its folder since then is kept in the backup.
func Restore(ctx context.Context, env Env) error {
	b, err := latest(env.backupRoot())
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(b, "manifest.json"))
	if err != nil {
		return fmt.Errorf("%s has no manifest.json: %v", b, err)
	}
	var man manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return fmt.Errorf("%s/manifest.json: %v", b, err)
	}
	stamp := env.Now.Format("20060102-150405")
	if env.Now.IsZero() {
		stamp = time.Now().Format("20060102-150405")
	}
	aside := filepath.Join(b, "after-"+stamp)
	for i := len(man.Done) - 1; i >= 0; i-- {
		s := man.Done[i]
		switch s.Op {
		case "move":
			if !exists(s.To) {
				continue
			}
			if exists(s.From) {
				// made since the migration (magpie wrote its folder again)
				if err := move(s.From, filepath.Join(aside, strings.TrimPrefix(filepath.ToSlash(s.From), "/"))); err != nil {
					return err
				}
			}
			if err := move(s.To, s.From); err != nil {
				return err
			}
		case "mkdir":
			if exists(s.To) {
				if err := move(s.To, filepath.Join(aside, strings.TrimPrefix(filepath.ToSlash(s.To), "/"))); err != nil {
					return err
				}
			}
		case "codex":
			if !exists(s.To) {
				continue
			}
			if exists(s.From) {
				if err := move(s.From, filepath.Join(aside, "codex-config.toml")); err != nil {
					return err
				}
			}
			if err := copyFile(s.To, s.From); err != nil {
				return err
			}
		}
	}
	return nil
}

// latest is the newest sp8-* backup.
func latest(root string) (string, error) {
	ents, _ := os.ReadDir(root)
	var names []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "sp8-") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no SP8 migration backup in %s", root)
	}
	sort.Strings(names)
	return filepath.Join(root, names[len(names)-1]), nil
}

// move renames from to to, making to's parent; across devices it copies
// and then moves the source aside next to the copy, so nothing is deleted.
func move(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	err := os.Rename(from, to)
	if err == nil {
		return nil
	}
	var le *os.LinkError
	if !errors.As(err, &le) || !strings.Contains(le.Err.Error(), "cross-device") {
		return err
	}
	if err := copyTree(from, to); err != nil {
		return err
	}
	return os.Rename(from, filepath.Join(filepath.Dir(from), "."+filepath.Base(from)+".moved-to-"+strings.ReplaceAll(filepath.ToSlash(to), "/", "_")))
}

// copyTree copies a file or a folder, keeping modes.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		default:
			return copyFile(p, dst)
		}
	})
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(to, st.Mode().Perm())
}
