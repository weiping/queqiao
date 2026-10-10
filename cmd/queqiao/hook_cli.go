package main

import (
	"fmt"
	"os"

	"github.com/weiping/queqiao/internal/harness"
	"github.com/weiping/queqiao/internal/harness/codex"
)

/**
 * `queqiao hook <user-prompt|pre-agent|post-bash|stop> --harness <name>`
 * (§6.6, SP7 §3.5).
 * Only codex has command hooks today; --harness keeps the door open for
 * the next one without re-trusting existing hook commands.
 */
func hookCmd(args []string) error {
	var sub, harnessName string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--harness":
			i++
			if i >= len(args) {
				return fmt.Errorf("--harness needs a name")
			}
			harnessName = args[i]
		default:
			if sub == "" {
				sub = args[i]
			} else {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
		}
	}
	if harnessName == "" {
		harnessName = "codex"
	}
	if harnessName != "codex" {
		return fmt.Errorf("unknown harness %q (only codex)", harnessName)
	}
	var h harness.Handler
	switch sub {
	case "user-prompt":
		h = codex.UserPrompt
	case "pre-agent":
		h = codex.PreAgent
	case "post-bash":
		h = codex.PostBash
	case "stop":
		h = codex.Stop
	default:
		return fmt.Errorf("queqiao hook takes user-prompt, pre-agent, post-bash or stop, not %q", sub)
	}
	// hooks always end 0, even on failure — main's error path must not run
	os.Exit(harness.Run(h))
	return nil
}
