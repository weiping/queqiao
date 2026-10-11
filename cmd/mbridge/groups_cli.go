package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/weiping/magpie-bridge/internal/magpie"
)

// explainUnlisted prints why magpie's /v1/models lacks one of mbridge's
// groups, as `magpie group <id>` tells it: the group is gone, or magpie
// skips every model in it (10-11: both of mb-perf's members were Copilot's,
// Copilot was off, and the old line said only "missing"). label leads the
// line ("performance group", or "" for status).
func explainUnlisted(ctx context.Context, mc *magpie.Client, label, id string) {
	full := magpie.GroupPrefix + id
	lead := full
	if label != "" {
		lead = label + " " + full
	}
	g, err := mc.Group(ctx, id)
	switch {
	case errors.Is(err, magpie.ErrNoGroup):
		if label != "" {
			fmt.Println(amber.Render("✗"), label, "missing in magpie:", full, muted.Render("· mbridge router init --groups-only --force"))
		} else {
			fmt.Println(amber.Render("✗"), full, "missing in magpie", muted.Render("· mbridge router init --groups-only --force"))
		}
	case err != nil:
		fmt.Println(amber.Render("✗"), lead+": magpie doesn't list it, and", err)
	case !g.Served():
		fmt.Println(amber.Render("✗"), lead+": magpie serves none of its models")
		for _, m := range g.Members {
			fmt.Println("   ", m.Model+":", muted.Render(m.NotServed))
		}
		fmt.Println("   ", muted.Render("· turn their provider back on, or give the group models magpie serves: magpie group set "+id+" models=<provider/model>,… (magpie models lists them)"))
	default:
		fmt.Println(amber.Render("✗"), lead+": magpie doesn't list it in /v1/models", muted.Render("· magpie group "+id))
	}
}
