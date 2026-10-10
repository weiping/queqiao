package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Codex clones a Git marketplace in full and gives up after 30 s; this
// repo's history (from the magpie fork) is ~96 MB, which times out on an
// ordinary connection (author, 2026-10-11). With --sparse Codex does a
// blob-less partial clone (~5 MB) and `upgrade` keeps the sparse paths.
// Every install line in the docs says so.
func TestCodexMarketplaceAddIsSparse(t *testing.T) {
	root := filepath.Join("..", "..")
	add := regexp.MustCompile(`codex plugin marketplace add weiping/magpie-bridge[^\n]*`)
	for _, f := range []string{"README.md", "clients/codex/README.md"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		lines := add.FindAllString(string(b), -1)
		if len(lines) == 0 {
			t.Errorf("%s has no Codex marketplace add line", f)
		}
		for _, l := range lines {
			if !strings.Contains(l, "--sparse .agents --sparse clients/codex") {
				t.Errorf("%s: %q clones the whole history (needs --sparse .agents --sparse clients/codex)", f, l)
			}
		}
	}
}
