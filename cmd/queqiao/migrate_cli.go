package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/weiping/queqiao/internal/magpie"
	"github.com/weiping/queqiao/internal/migrate"
)

// migrateEnv is this computer's install, as migrate sees it.
var migrateEnv = func() migrate.Env {
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		cfg = filepath.Join(home, ".config")
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(cache) {
		cache = filepath.Join(home, ".cache")
	}
	sys, _ := os.UserCacheDir()
	return migrate.Env{
		Home: home, GOOS: runtime.GOOS, ConfigHome: cfg, CacheHome: cache, SystemCache: sys, Now: time.Now(),
		OldGatewayRunning: oldGatewayRunning,
		MagpieVersion:     func(ctx context.Context) (string, error) { return newMagpie("").Version(ctx) },
	}
}

// oldGatewayRunning: qq-v0.1.x served /v1/queqiao/router on magpie's port;
// official magpie answers it 404.
func oldGatewayRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, magpie.DefaultURL+"/v1/queqiao/router", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

var confirmIn = bufio.NewReader(os.Stdin)

// migrateCmd is `queqiao migrate [--dry-run] [--yes]` and `queqiao migrate
// restore` (SP8 §7.1).
func migrateCmd(args []string) error {
	ctx := context.Background()
	dry, yes := false, false
	for _, a := range args {
		switch a {
		case "restore":
			if len(args) != 1 {
				return fmt.Errorf("usage: queqiao migrate restore")
			}
			if err := migrate.Restore(ctx, migrateEnv()); err != nil {
				return err
			}
			fmt.Println(green.Render("✓"), "restored to before the migration; qq-v0.1.x can run again")
			return nil
		case "--dry-run":
			dry = true
		case "--yes":
			yes = true
		default:
			return fmt.Errorf("usage: queqiao migrate [--dry-run] [--yes] | queqiao migrate restore")
		}
	}
	plan, err := migrate.Prepare(ctx, migrateEnv())
	if err != nil {
		return err
	}
	fmt.Print(plan.String())
	if dry {
		fmt.Println(muted.Render("dry run: nothing changed"))
		return nil
	}
	if !yes {
		fmt.Print("go ahead? [y/N] ")
		line, _ := confirmIn.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return fmt.Errorf("not migrated")
		}
	}
	if err := plan.Apply(ctx); err != nil {
		return err
	}
	fmt.Println(green.Render("✓"), "migrated; the backup is", plan.Backup)
	fmt.Println("  next: start magpie (magpie, or magpie autostart on), then queqiao service install")
	fmt.Println("  Codex: codex -p queqiao · undo all of this: queqiao migrate restore")
	return nil
}
