package stats

import (
	"testing"
	"time"
)

func TestRunNeedsStatsHost(t *testing.T) {
	t.Setenv("MAGPIE_STATS_HOST", "")
	t.Setenv("MAGPIE_NO_STATS", "1") // nothing leaves the sandbox while it fails
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	done := make(chan struct{})
	go func() { Run("1.2.3", "serve"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going for a release without MAGPIE_STATS_HOST")
	}
}
