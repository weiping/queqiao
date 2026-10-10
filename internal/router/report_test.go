package router

import (
	"testing"
	"time"

	"github.com/yetone/magpie/internal/magpie"
)

/**
 * §11's SP5 acceptance: the report's statistics over synthetic
 * usage.jsonl and router.jsonl rows, with hand-computable numbers.
 *
 * Fixture: 3 router-arm sessions and 3 control-arm (shadow) sessions.
 * Prices are fixed ($1/M in, $2/M out, $0.1/M cache read, $3/M cache
 * write) so every cost below is exact arithmetic.
 */

var testNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// testCost is what magpie would put in cost_usd at these prices:
// in $1/M, out $2/M, cacheRead $0.1/M, cacheWrite $3/M.
func testCost(in, out, cacheR, cacheW int) float64 {
	return (float64(in)*1 + float64(out)*2 + float64(cacheR)*0.1 + float64(cacheW)*3) / 1e6
}

func at(off time.Duration) time.Time { return testNow.Add(-off) }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func ev(kind, session string, t time.Time, mutate func(*Event)) Event {
	e := Event{Kind: kind, Session: session, Time: ts(t)}
	if mutate != nil {
		mutate(&e)
	}
	return e
}

func rec(session string, t time.Time, inTok, outTok, cacheW int) magpie.UsageRow {
	return magpie.UsageRow{Session: session, Time: t, Provider: "p", Model: "m",
		Input: inTok, Output: outTok, CacheWrite: cacheW, Status: 200,
		CostUSD: testCost(inTok, outTok, 0, cacheW), Priced: true}
}

func TestAggregateOverSyntheticLedgers(t *testing.T) {
	// router arm: 3 sessions
	r1 := "sess-r1"
	r2 := "sess-r2"
	r3 := "sess-r3"
	// control arm: 3 sessions (their events arrive as shadow)
	c1 := "sess-c1"
	c2 := "sess-c2"
	c3 := "sess-c3"

	events := []Event{
		// router sessions: one decide each, tiers fast/fast/balanced
		ev("decide", r1, at(1*time.Hour), func(e *Event) { e.Arm, e.Tier, e.Harness = "router", "fast", "codex" }),
		ev("decide", r2, at(2*time.Hour), func(e *Event) { e.Arm, e.Tier, e.Harness = "router", "fast", "codex" }),
		ev("decide", r3, at(3*time.Hour), func(e *Event) { e.Arm, e.Tier, e.Harness = "router", "balanced", "codex" }),
		// control sessions: shadow events (tier = control tier, shadow = the pick)
		ev("shadow", c1, at(1*time.Hour), func(e *Event) { e.Arm, e.Tier, e.ShadowTier = "control", "performance", "fast" }),
		ev("shadow", c2, at(2*time.Hour), func(e *Event) { e.Arm, e.Tier, e.ShadowTier = "control", "performance", "fast" }),
		ev("shadow", c3, at(3*time.Hour), func(e *Event) { e.Arm, e.Tier, e.ShadowTier = "control", "performance", "balanced" }),
		// one gateway-mode fallback decide in the router arm
		ev("decide", r1, at(90*time.Minute), func(e *Event) { e.Arm, e.Harness = "router", "gateway" }),
		// hints: 3 consumed in router arm, 2 in control
		ev("hint_consumed", r1, at(61*time.Minute), nil),
		ev("hint_consumed", r2, at(121*time.Minute), nil),
		ev("hint_consumed", r3, at(181*time.Minute), nil),
		ev("hint_consumed", c1, at(61*time.Minute), nil),
		ev("hint_consumed", c2, at(121*time.Minute), nil),
		// feedback: r1 and r3 opened PRs (r1's is MERGED via PRStates,
		// r3's is OPEN); c1 opened one merged via the pr_merged fallback;
		// r2 switched models by hand
		ev("feedback", r1, at(50*time.Minute), func(e *Event) { e.Extra = "pr_created https://github.com/a/b/pull/1" }),
		ev("feedback", r3, at(170*time.Minute), func(e *Event) { e.Extra = "pr_created https://github.com/a/b/pull/3" }),
		ev("feedback", c1, at(50*time.Minute), func(e *Event) { e.Extra = "pr_created https://github.com/a/b/pull/9" }),
		ev("feedback", c1, at(30*time.Minute), func(e *Event) { e.Extra = "pr_merged https://github.com/a/b/pull/9" }),
		ev("feedback", r2, at(100*time.Minute), func(e *Event) { e.Extra = "manual_model_switch other/m1" }),
		ev("feedback", r2, at(101*time.Minute), func(e *Event) { e.Extra = "manual_model_switch other/m2" }),
		// outside the window: ignored entirely
		ev("decide", "sess-old", at(30*24*time.Hour), func(e *Event) { e.Arm, e.Tier = "router", "fast" }),
	}

	records := []magpie.UsageRow{
		// r1: 1000 in + 1000 out + 5000 cache write
		rec(r1, at(59*time.Minute), 1000, 1000, 5000),
		// r2: 2000 in, 500 out (twice: two requests, one session)
		rec(r2, at(119*time.Minute), 2000, 500, 0),
		rec(r2, at(120*time.Minute), 2000, 500, 0),
		// r3: 10000 in, 20000 out, big cache write
		rec(r3, at(179*time.Minute), 10000, 20000, 100000),
		// control sessions
		rec(c1, at(59*time.Minute), 1000, 1000, 0),
		rec(c2, at(119*time.Minute), 4000, 1000, 0),
		rec(c3, at(179*time.Minute), 6000, 3000, 20000),
		// no session (a probe): excluded
		rec("", at(10*time.Minute), 999, 999, 0),
		// outside the window: excluded
		rec(r1, at(30*24*time.Hour), 999, 999, 0),
		// a failed request: excluded from cost
		{Session: r1, Time: at(58 * time.Minute), Provider: "p", Model: "m", Input: 500, Output: 500, Status: 500},
	}

	in := ReportInput{
		Records: records,
		Events:  events,
		Now:     testNow,
		Since:   14 * 24 * time.Hour,
		PRStates: map[string]string{
			"https://github.com/a/b/pull/1": "MERGED",
			"https://github.com/a/b/pull/3": "OPEN",
			// pull/9 absent: the pr_merged fallback decides
		},
	}
	rep := Aggregate(in)

	// --- router arm ---
	r := rep.Router
	if r.Sessions != 3 {
		t.Fatalf("router sessions: %d", r.Sessions)
	}
	// costs per session: r1 = (1000*1 + 1000*2 + 5000*3)/1e6 = 0.018
	// r2 = 2×(2000*1 + 500*2)/1e6 = 0.006 ; r3 = (10000+40000+300000)/1e6 = 0.35
	if got := r.CostMedian; abs(got-0.018) > 1e-9 {
		t.Fatalf("router median: %v", got)
	}
	if got := r.CostMean; abs(got-(0.018+0.006+0.35)/3) > 1e-9 {
		t.Fatalf("router mean: %v", got)
	}
	if got := r.CostP90; abs(got-0.35) > 1e-9 { // nearest rank ⌈2.7⌉=3rd of 3
		t.Fatalf("router p90: %v", got)
	}
	if !(r.CostCILo <= 0.018 && 0.018 <= r.CostCIHi) {
		t.Fatalf("router CI must contain the median: [%v,%v]", r.CostCILo, r.CostCIHi)
	}
	// tier distribution over turns: fast 2, balanced 1 (gateway decide has no tier)
	if f := r.TierDistribution["fast"]; abs(f-2.0/3.0) > 1e-9 {
		t.Fatalf("router tier fast: %v", f)
	}
	// PRs: 2 of 3 opened, 1 merged (pull/1 MERGED; pull/3 OPEN)
	if r.PRSessions != 2 || r.MergedSessions != 1 {
		t.Fatalf("router PRs: %d merged %d", r.PRSessions, r.MergedSessions)
	}
	// manual: 1 of 3 (r2's two events dedup by session)
	if r.ManualSwitchSessions != 1 {
		t.Fatalf("router manual: %d", r.ManualSwitchSessions)
	}
	// hint rate parts: 3 consumed vs 1 gateway decide
	if r.HintConsumed != 3 || r.GatewayDecides != 1 {
		t.Fatalf("router hints: %d/%d", r.HintConsumed, r.GatewayDecides)
	}
	// cache write share, estimated from magpie's cost by the cache write's
	// share of the input-side tokens (magpie's CSV has no per-kind cost):
	// r1 0.018×5000/6000 = 0.015, r3 0.35×100000/110000 ≈ 0.3182, of 0.374
	if got := r.CacheWriteCost / r.TotalCost; abs(got-(0.015+0.35*100000/110000)/0.374) > 1e-9 {
		t.Fatalf("router cache share: %v (%v/%v)", got, r.CacheWriteCost, r.TotalCost)
	}
	if r.UnpricedRequests != 0 {
		t.Fatalf("router unpriced: %d", r.UnpricedRequests)
	}

	// --- control arm ---
	c := rep.Control
	if c.Sessions != 3 {
		t.Fatalf("control sessions: %d", c.Sessions)
	}
	// shadow distribution is the router's own picks: fast 2, balanced 1
	if f := c.ShadowDistribution["fast"]; abs(f-2.0/3.0) > 1e-9 {
		t.Fatalf("control shadow fast: %v", f)
	}
	// served tier distribution is all performance (the control tier)
	if f := c.TierDistribution["performance"]; f != 1 {
		t.Fatalf("control tier performance: %v", f)
	}
	// c1 merged via the pr_merged fallback
	if c.PRSessions != 1 || c.MergedSessions != 1 {
		t.Fatalf("control PRs: %d merged %d", c.PRSessions, c.MergedSessions)
	}
	if c.HintConsumed != 2 || c.GatewayDecides != 0 {
		t.Fatalf("control hints: %d/%d", c.HintConsumed, c.GatewayDecides)
	}

	// --- comparison ---
	// merged rate 1/3 vs 1/3 → z = 0
	if rep.MergedZ != 0 {
		t.Fatalf("merged z: %v", rep.MergedZ)
	}
	if !rep.SampleShort {
		t.Fatal("3+3 sessions must flag SampleShort")
	}
}

func TestAggregateExcludesUnpricedAndOldArms(t *testing.T) {
	events := []Event{
		ev("decide", "s-a", at(1*time.Hour), func(e *Event) { e.Arm, e.Tier = "router", "fast" }),
		ev("decide", "s-b", at(1*time.Hour), func(e *Event) { e.Arm, e.Tier = "router", "fast" }),
	}
	records := []magpie.UsageRow{
		rec("s-a", at(59*time.Minute), 1000, 1000, 0), // priced
		rec("s-b", at(59*time.Minute), 1000, 1000, 0), // priced
	}
	for i := range records {
		records[i].CostUSD, records[i].Priced = 0, false // nothing priced
	}
	in := ReportInput{Records: records, Events: events, Now: testNow, Since: 24 * time.Hour}
	rep := Aggregate(in)
	if rep.Router.Sessions != 2 {
		t.Fatalf("sessions: %d", rep.Router.Sessions)
	}
	if rep.Router.UnpricedRequests != 2 || rep.Router.TotalCost != 0 {
		t.Fatalf("unpriced: %d cost %v", rep.Router.UnpricedRequests, rep.Router.TotalCost)
	}
	if rep.Router.CostMedian != 0 {
		t.Fatalf("median of no costs: %v", rep.Router.CostMedian)
	}
}

func TestStatsHelpers(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 100}
	if median(xs) != 3 || mean(xs) != 22 || p90(xs) != 100 {
		t.Fatalf("median/mean/p90")
	}
	if m := median([]float64{1, 2}); abs(m-1.5) > 1e-9 {
		t.Fatalf("even median: %v", m)
	}
	if m := p90([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}); abs(m-9) > 1e-9 {
		t.Fatalf("p90 of 10: %v", m) // ⌈9⌉=9th smallest = 9
	}
	lo, hi := bootstrapMedianCI(xs, 1000)
	if lo > hi {
		t.Fatalf("CI inverted: %v %v", lo, hi)
	}
	// a clear difference is a clear z
	if z := twoProportionZ(10, 10, 0, 10); z < 3 {
		t.Fatalf("z for 100%% vs 0%%: %v", z)
	}
	if z := twoProportionZ(0, 0, 0, 10); z != 0 {
		t.Fatalf("z with empty arm: %v", z)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// SP7 §6.2: an arm's report carries the escalation rates by source and the
// under-tier rate among the turns that were not escalated.
func TestReportEscalationRatesAndKeptUnderRate(t *testing.T) {
	now := time.Now().UTC()
	clock := now.Add(-time.Hour)
	events := []Event{}
	add := func(turnID, reason string, diss float64, hasDiss, wouldReview bool) {
		clock = clock.Add(time.Minute)
		ev := Event{Kind: "decide", Time: clock.Format(time.RFC3339), Session: "s1",
			Harness: "codex", Agent: "main", Arm: "router", TurnID: turnID,
			Tier: TierFast, ClassifiedTier: TierFast, Reason: reason, WouldReview: wouldReview,
			Classifier: "typeset/jev-latest"}
		c := 0.9
		ev.TierConfidence = &c
		if hasDiss {
			ev.Dissatisfied = &diss
		}
		events = append(events, ev)
	}
	add("t1", "R3-escalate", 0, false, false)
	add("t2", "R3-tools", 0, false, false)
	add("t3", "R3-review", 0, false, false)
	add("t4", "R3-review", 0, false, false)
	// the six kept turns; t5 carries the shadow review hit, and t5..t7 are
	// labelled under-tiered by the next turns' dissatisfaction
	add("t5", "R6-adopt", 0, false, true)
	add("t6", "R6-adopt", 0.9, true, false)
	add("t7", "R6-adopt", 0.9, true, false)
	add("t8", "R6-adopt", 0.9, true, false)
	add("t9", "R6-adopt", 0, false, false)
	add("t10", "R6-adopt", 0, false, false)

	rep := Aggregate(ReportInput{Events: events, Now: now, Since: 24 * time.Hour})
	if got := rep.Router.EscalateRates["R3-review"]; got != 0.2 {
		t.Fatalf("R3-review rate = %v, want 0.2", got)
	}
	if got := rep.Router.EscalateRates["would_review"]; got != 0.1 {
		t.Fatalf("would_review rate = %v, want 0.1", got)
	}
	if rep.Router.UnderRateKept != 0.5 {
		t.Fatalf("UnderRateKept = %v, want 0.5", rep.Router.UnderRateKept)
	}
	if len(rep.Router.ScoreBands["tier"]) != 5 {
		t.Fatalf("score bands: %+v", rep.Router.ScoreBands)
	}
}
