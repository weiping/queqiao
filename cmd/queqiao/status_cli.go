package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/weiping/queqiao/internal/router"
)

// statusCmd is `queqiao status` (and `queqiao router status`): router.json,
// magpie, the tier groups in it, queqiaod and its latest decisions. It
// reports what is wrong; it does not fail for it.
func statusCmd(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("status takes no arguments")
	}
	cfg, err := router.Load(routerJSONPath(), "")
	if err != nil {
		fmt.Println(amber.Render("✗"), "router.json:", err, muted.Render("· queqiao router init writes one"))
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
		for _, g := range want {
			if err == nil && !slices.Contains(groups, g) {
				fmt.Println(amber.Render("✗"), "group/"+g, "missing in magpie", muted.Render("· queqiao router init --groups-only --force"))
			}
		}
	}
	c := &http.Client{Timeout: 2 * time.Second}
	res, err := c.Get("http://" + cfg.Listen + "/v1/queqiao/router")
	if err != nil {
		fmt.Println(amber.Render("✗"), "queqiaod isn't running on "+cfg.Listen, muted.Render("· queqiao service install, or queqiao serve"))
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
		fmt.Println(amber.Render("✗"), "queqiaod on", cfg.Listen, "answered something else:", err)
		return nil
	}
	if !body.Valid {
		fmt.Println(amber.Render("✗"), "queqiaod on", cfg.Listen, "runs without a router:", body.Error)
		return nil
	}
	fmt.Println(green.Render("✓"), "queqiaod on", cfg.Listen)
	if len(body.Decisions) == 0 {
		fmt.Println(muted.Render("  no decisions yet"))
	}
	for _, d := range body.Decisions {
		fmt.Printf("  %s → %s (%s)\n", d.Session, d.Tier, d.Reason)
	}
	return nil
}

// versionCmd is `queqiao version`: queqiao's and the magpie it finds.
func versionCmd(args []string) error {
	fmt.Println("queqiao", version)
	if v, err := magpieFor().Version(context.Background()); err == nil && v != "" {
		fmt.Println(v)
	} else {
		fmt.Println(muted.Render("magpie: not found (" + fmt.Sprint(err) + ")"))
	}
	return nil
}
