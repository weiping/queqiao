package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/weiping/magpie-bridge/internal/magpie"
	"net/http"
	"slices"
	"time"

	"github.com/weiping/magpie-bridge/internal/router"
)

// statusCmd is `mbridge status` (and `mbridge router status`): router.json,
// magpie, the tier groups in it, mbridge and its latest decisions. It
// reports what is wrong; it does not fail for it.
func statusCmd(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("status takes no arguments")
	}
	cfg, err := router.Load(routerJSONPath(), "")
	if err != nil {
		fmt.Println(amber.Render("✗"), "router.json:", err, muted.Render("· mbridge router init writes one"))
		return nil
	}
	fmt.Println(green.Render("✓"), "router.json", muted.Render(routerJSONPath()), muted.Render("· review "+string(cfg.Review.Mode)))
	ctx := context.Background()
	mc := newMagpie(cfg.MagpieURL)
	if err := mc.Health(ctx); err != nil {
		fmt.Println(amber.Render("✗"), "magpie isn't answering at", cfg.MagpieURL+":", err, muted.Render("· start it: magpie serve, or its app"))
	} else {
		v, _ := mc.Version(ctx)
		fmt.Println(green.Render("✓"), "magpie at", cfg.MagpieURL, muted.Render(v))
		groups, err := mc.Groups(ctx)
		if err != nil {
			fmt.Println(amber.Render("✗"), "magpie groups:", err)
		}
		want := []string{cfg.RouterGroup}
		for _, tier := range []router.Tier{router.TierFast, router.TierBalanced, router.TierPerformance} {
			want = append(want, cfg.Tiers[tier].Group)
		}
		// a group magpie has but doesn't serve (every member skipped) is as
		// broken as a missing one: /v1/models leaves it out
		listed := map[string]bool{}
		models, lerr := mc.ModelList(ctx)
		for _, m := range models {
			listed[m.ID] = true
		}
		for _, g := range want {
			switch {
			case err == nil && !slices.Contains(groups, g):
				fmt.Println(amber.Render("✗"), "group/"+g, "missing in magpie", muted.Render("· mbridge router init --groups-only --force"))
			case lerr == nil && !listed[magpie.GroupPrefix+g]:
				explainUnlisted(ctx, mc, "", g)
			}
		}
	}
	c := &http.Client{Timeout: 2 * time.Second}
	res, err := c.Get("http://" + cfg.Listen + "/v1/bridge/router")
	if err != nil {
		fmt.Println(amber.Render("✗"), "mbridge isn't running on "+cfg.Listen, muted.Render("· mbridge service install, or mbridge serve"))
		return nil
	}
	defer res.Body.Close()
	var body struct {
		Valid     bool   `json:"valid"`
		Error     string `json:"error"`
		Decisions []struct {
			Session string `json:"session"`
			Tier    string `json:"tier"`
			Reason  string `json:"reason"`
		} `json:"decisions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		fmt.Println(amber.Render("✗"), "mbridge on", cfg.Listen, "answered something else:", err)
		return nil
	}
	if !body.Valid {
		fmt.Println(amber.Render("✗"), "mbridge on", cfg.Listen, "runs without a router:", body.Error)
		return nil
	}
	fmt.Println(green.Render("✓"), "mbridge on", cfg.Listen)
	if len(body.Decisions) == 0 {
		fmt.Println(muted.Render("  no decisions yet"))
	}
	for _, d := range body.Decisions {
		fmt.Printf("  %s → %s (%s)\n", d.Session, d.Tier, d.Reason)
	}
	return nil
}

// versionCmd is `mbridge version`: mbridge's and the magpie it finds.
func versionCmd(args []string) error {
	fmt.Println("mbridge", version)
	if v, err := magpieFor().Version(context.Background()); err == nil && v != "" {
		fmt.Println(v)
	} else {
		fmt.Println(muted.Render("magpie: not found (" + fmt.Sprint(err) + ")"))
	}
	return nil
}
