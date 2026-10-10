package runcmd

import (
	"context"
	"testing"
)

// mbridge runs from a scheduled task with no console; the commands it
// starts (magpie, gh) must not each open a window.
func TestNoWindowWithoutConsole(t *testing.T) {
	old := hasConsole
	hasConsole = func() bool { return false }
	t.Cleanup(func() { hasConsole = old })
	cmd := CommandContext(context.Background(), "cmd", "/c", "echo")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags lack CREATE_NO_WINDOW: %+v", cmd.SysProcAttr)
	}
}
