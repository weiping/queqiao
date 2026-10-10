package migrate

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnderBackupIsRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		return // TestUnderBackupDropsTheVolumeColon
	}
	if got := underBackup("/home/u/.config/magpie"); got != filepath.Join("home", "u", ".config", "magpie") {
		t.Fatalf("got %q", got)
	}
}
