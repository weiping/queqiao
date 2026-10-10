// Command queqiao routes each agent turn to a fast, balanced or
// performance model through official magpie (SP8): queqiaod serves the
// router's endpoints and a proxy for Codex; the rest of the commands set
// it up and report on it.
package main

import (
	"fmt"
	"os"
)

// version is set at build time (-X main.version=…).
var version = "dev"

const usageText = `queqiao — per-turn model routing for coding agents, on official magpie

  queqiao serve                         run queqiaod in the foreground (the service runs it)
  queqiao service install|uninstall|status  run queqiaod at login
  queqiao status                        magpie, queqiaod, the tier groups and the latest decisions
  queqiao migrate [--dry-run] [--yes]   hand a qq-v0.1.x install's data back to official magpie
  queqiao migrate restore               undo the last migrate
  queqiao router init --preset <frontier|anthropic|cn> [--groups-only] [--force]
                                        tier groups in magpie, router.json, and the agents' wiring
  queqiao router check [--yes]          the tier groups exist and are big enough (--yes: live, billed)
  queqiao router report [--since 14d] [--json]   the experiment report
  queqiao router calibrate [--since 14d] [--score tier|dissatisfied|review] [--harness h] [--agent main|sub] [--csv]
  queqiao hook <user-prompt|pre-agent|post-bash|stop> --harness codex   Codex's command hooks
  queqiao update                        install the newest queqiao release
  queqiao version                       queqiao's version and magpie's

queqiao needs official magpie (https://github.com/yetone/magpie) installed and running.
`

func main() { os.Exit(run(os.Args[1:])) }

// run runs one command and returns the exit code.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Print(usageText)
		return 0
	}
	var err error
	switch args[0] {
	case "serve":
		err = serveCmd(args[1:])
	case "service":
		err = serviceCmd(args[1:])
	case "status":
		err = statusCmd(args[1:])
	case "migrate":
		err = migrateCmd(args[1:])
	case "router":
		err = routerCmd(args[1:])
	case "hook":
		err = hookCmd(args[1:])
	case "update":
		err = updateCmd(args[1:])
	case "version", "--version", "-v":
		err = versionCmd(args[1:])
	case "help", "--help", "-h":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "queqiao: no command %q (magpie's own commands are magpie's: magpie %s)\n\n%s", args[0], args[0], usageText)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "queqiao:", err)
		return 1
	}
	return 0
}
