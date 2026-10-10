package migrate

import "testing"

// A Windows path's volume becomes a plain folder inside the backup: a
// colon in a folder name is refused there.
func TestUnderBackupDropsTheVolumeColon(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\x\.config\magpie`: `C\Users\x\.config\magpie`,
		`\\srv\share\x`:             `srv\share\x`,
	} {
		if got := underBackup(in); got != want {
			t.Errorf("underBackup(%q) = %q, want %q", in, got, want)
		}
	}
}
