package router

import (
	"strings"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/usage"
)

/**
 * The §9 experiment report, as pure aggregation over the two ledgers:
 * usage records (per-request cost, keyed by session) and router events
 * (decisions, hint consumptions, feedback). No IO happens here — the CLI
 * reads the files and resolves PR states, then calls Aggregate.
 *
 * Grouping follows the plan's review: a session's arm is the arm of its
 * LAST decide-or-shadow event (control sessions appear only as shadow,
 * with the router's own pick in ShadowTier).
 */

// ReportInput is everything Aggregate needs, already loaded.
type ReportInput struct {
	Records []usage.Record
	Events  []Event
	Now     time.Time
	Since   time.Duration
	// PriceOf prices a (provider, model) pair; nil means unpriced — such
	// requests are excluded from cost metrics and counted instead.
	PriceOf func(provider, model string) *catalog.Price
	// PRStates maps a PR URL to its state at report time (OPEN / MERGED /
	// CLOSED), already resolved by the CLI (gh, feedback fallback).
	PRStates map[string]string
}

// ArmReport is one arm's §9 metrics.
type ArmReport struct {
	Arm                  string
	Sessions             int
	CostMedian           float64
	CostMean             float64
	CostP90              float64
	CostCILo, CostCIHi   float64
	TierDistribution     map[string]float64 // fractions over the arm's turns
	ShadowDistribution   map[string]float64 // control arm: the router's own picks
	PRSessions           int
	MergedSessions       int
	ManualSwitchSessions int
	HintConsumed         int
	GatewayDecides       int
	CacheWriteCost       float64
	TotalCost            float64
	UnpricedRequests     int
}

// Report is both arms plus the comparison the §9 text asks for.
type Report struct {
	Router  ArmReport
	Control ArmReport
	// MergedZ is the two-proportion z on merged-PR session rates
	// (router − control); significant only with enough samples (§9).
	MergedZ float64
	// SampleShort is true when either arm has fewer than 100 sessions:
	// §9 then allows only cost and manual-switch rates as conclusions.
	SampleShort bool
}

// Aggregate computes the report. Sessions without a routed event (no
// decide/shadow carrying an arm) are not experiment data and drop out.
func Aggregate(in ReportInput) Report {
	since := in.Now.Add(-in.Since)

	// session → arm, from the last decide/shadow event that carries one
	armOf := map[string]string{}
	for _, ev := range in.Events {
		if ev.Kind != "decide" && ev.Kind != "shadow" || ev.Arm == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, ev.Time)
		if err == nil && !t.Before(since) {
			armOf[ev.Session] = ev.Arm
		}
	}

	prSessions := map[string]bool{} // session opened ≥1 PR
	merged := map[string]bool{}     // session has ≥1 MERGED pr (given PRStates)
	manual := map[string]bool{}     // session manually switched ≥1 time
	hintByArm := map[string]int{}
	gatewayByArm := map[string]int{}

	for _, ev := range in.Events {
		t, err := time.Parse(time.RFC3339, ev.Time)
		if err != nil || t.Before(since) {
			continue
		}
		switch ev.Kind {
		case "feedback":
			switch {
			case strings.HasPrefix(ev.Extra, "pr_created"):
				prSessions[ev.Session] = true
				// terminal state: PRStates (resolved by the CLI) wins;
				// the session's own pr_merged feedback is the fallback
				url := strings.TrimSpace(strings.TrimPrefix(ev.Extra, "pr_created"))
				state, ok := in.PRStates[url]
				if !ok && hasMergedFallback(in.Events, since, ev.Session) {
					state = "MERGED"
				}
				if state == "MERGED" {
					merged[ev.Session] = true
				}
			case strings.HasPrefix(ev.Extra, "manual_model_switch"):
				manual[ev.Session] = true
			}
		case "hint_consumed":
			if arm, ok := armOf[ev.Session]; ok {
				hintByArm[arm]++
			}
		case "decide":
			if ev.Harness == "gateway" {
				if arm, ok := armOf[ev.Session]; ok {
					gatewayByArm[arm]++
				}
			}
		}
	}

	// per-session costs and per-turn tier counts
	costs := map[string][]float64{}
	costByArm := map[string]map[string]float64{}
	cacheWrite := map[string]float64{}
	total := map[string]float64{}
	unpriced := map[string]int{}
	turnTiers := map[string]map[string]int{}
	shadowTiers := map[string]map[string]int{}

	for _, ev := range in.Events {
		t, err := time.Parse(time.RFC3339, ev.Time)
		if err != nil || t.Before(since) {
			continue
		}
		if ev.Kind == "decide" || ev.Kind == "shadow" {
			if arm, ok := armOf[ev.Session]; ok && ev.Tier != "" {
				if turnTiers[arm] == nil {
					turnTiers[arm] = map[string]int{}
				}
				turnTiers[arm][string(ev.Tier)]++
				if ev.Kind == "shadow" && ev.ShadowTier != "" {
					if shadowTiers[arm] == nil {
						shadowTiers[arm] = map[string]int{}
					}
					shadowTiers[arm][string(ev.ShadowTier)]++
				}
			}
		}
	}

	for _, r := range in.Records {
		if r.Session == "" || r.Time.Before(since) || r.Status >= 300 {
			continue
		}
		arm, ok := armOf[r.Session]
		if !ok {
			continue
		}
		p := in.PriceOf(r.Provider, r.Model)
		if p == nil {
			unpriced[arm]++
			continue
		}
		c := p.Cost(r.Input, r.Output, r.CacheRead, r.CacheWrite)
		if costByArm[arm] == nil {
			costByArm[arm] = map[string]float64{}
		}
		costByArm[arm][r.Session] += c
		cacheWrite[arm] += float64(r.CacheWrite) * p.CacheWrite / 1e6
		total[arm] += c
	}
	for arm, bySession := range costByArm {
		for _, c := range bySession {
			costs[arm] = append(costs[arm], c)
		}
	}

	arm := func(name string) ArmReport {
		rep := ArmReport{Arm: name}
		for s, a := range armOf {
			if a == name {
				rep.Sessions++
				if prSessions[s] {
					rep.PRSessions++
				}
				if merged[s] {
					rep.MergedSessions++
				}
				if manual[s] {
					rep.ManualSwitchSessions++
				}
			}
		}
		cs := costs[name]
		rep.CostMedian = median(cs)
		rep.CostMean = mean(cs)
		rep.CostP90 = p90(cs)
		rep.CostCILo, rep.CostCIHi = bootstrapMedianCI(cs, 1000)
		rep.TierDistribution = fractions(turnTiers[name])
		rep.ShadowDistribution = fractions(shadowTiers[name])
		rep.HintConsumed = hintByArm[name]
		rep.GatewayDecides = gatewayByArm[name]
		rep.CacheWriteCost = cacheWrite[name]
		rep.TotalCost = total[name]
		rep.UnpricedRequests = unpriced[name]
		return rep
	}

	out := Report{Router: arm("router"), Control: arm("control")}
	out.MergedZ = twoProportionZ(
		out.Router.MergedSessions, out.Router.Sessions,
		out.Control.MergedSessions, out.Control.Sessions)
	out.SampleShort = out.Router.Sessions < 100 || out.Control.Sessions < 100
	return out
}

func fractions(counts map[string]int) map[string]float64 {
	if len(counts) == 0 {
		return nil
	}
	var n int
	for _, c := range counts {
		n += c
	}
	out := make(map[string]float64, len(counts))
	for k, c := range counts {
		out[k] = float64(c) / float64(n)
	}
	return out
}

// hasMergedFallback reports whether the session also logged a pr_merged
// feedback event inside the window (the §6.4 kinds SP5 falls back to
// when gh is missing).
func hasMergedFallback(events []Event, since time.Time, session string) bool {
	for _, ev := range events {
		if ev.Session != session || ev.Kind != "feedback" {
			continue
		}
		if strings.HasPrefix(ev.Extra, "pr_merged") {
			if t, err := time.Parse(time.RFC3339, ev.Time); err == nil && !t.Before(since) {
				return true
			}
		}
	}
	return false
}
