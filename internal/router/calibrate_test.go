package router

import (
	"fmt"
	"testing"
)

// bandSpec is one group of synthetic turns: a fixed tier confidence and
// how many of them are under-tiered.
type bandSpec struct {
	conf  float64
	n     int
	under int
}

// tierEvents builds one session's decide events for the given bands, in
// the order given. The under-tier label rides on the NEXT turn's
// dissatisfied score (§5.2), so the last turn is never labelled under.
func tierEvents(bands []bandSpec) []Event {
	// expand to per-turn scores and under flags
	var confs []float64
	var unders []bool
	for _, b := range bands {
		for i := 0; i < b.n; i++ {
			confs = append(confs, b.conf)
			unders = append(unders, i < b.under)
		}
	}
	events := make([]Event, 0, len(confs))
	for i, c := range confs {
		ev := Event{Kind: "decide", Session: "s1", Agent: "main", Arm: "router",
			Harness: "claude-code", TurnID: fmt.Sprintf("t%d", i+1),
			Tier: TierFast, ClassifiedTier: TierFast, Reason: "R6-adopt",
			Classifier: "typeset/jev-latest"}
		conf := c
		ev.TierConfidence = &conf
		if i > 0 { // the first turn has no previous answer
			d := 0.0
			if unders[i-1] {
				d = 0.9
			}
			ev.Dissatisfied = &d
		}
		events = append(events, ev)
	}
	return events
}

func bandN(sc ScoreCalibration, i int) int { return sc.Bands[i].N }

func TestCalibrateBandsAndUnderRate(t *testing.T) {
	events := tierEvents([]bandSpec{
		{0.97, 80, 1}, {0.9, 50, 2}, {0.78, 40, 4}, {0.6, 20, 7}, {0.3, 10, 6},
	})
	got := Calibrate(events, testConfig(""), CalibrateFilter{Score: "tier"})
	if len(got) != 1 {
		t.Fatalf("calibrations = %d, want 1", len(got))
	}
	sc := got[0]
	for i, want := range []int{80, 50, 40, 20, 10} {
		if bandN(sc, i) != want {
			t.Fatalf("band %d N = %d, want %d", i, bandN(sc, i), want)
		}
	}
	for i, want := range []float64{0.0125, 0.04, 0.1, 0.35, 0.6} {
		if r := sc.Bands[i].UnderRate; r < want-1e-9 || r > want+1e-9 {
			t.Fatalf("band %d UnderRate = %v, want %v", i, r, want)
		}
	}
}

func TestCalibrateSuggestsAtJump(t *testing.T) {
	events := tierEvents([]bandSpec{
		{0.97, 410, 4}, {0.9, 270, 11}, {0.78, 180, 20}, {0.6, 90, 31}, {0.3, 50, 30},
	})
	sc := Calibrate(events, testConfig(""), CalibrateFilter{Score: "tier"})[0]
	if sc.Suggested == nil || *sc.Suggested != 0.70 {
		t.Fatalf("suggested = %v, want 0.70", sc.Suggested)
	}
	if sc.SampleShort {
		t.Fatal("SampleShort on a well-sampled score")
	}
	if sc.ProjEscalateRate == 0 && sc.ProjUnderRateKept == 0 {
		t.Fatal("projection was not computed")
	}
}

func TestCalibrateSampleShort(t *testing.T) {
	events := tierEvents([]bandSpec{
		{0.97, 80, 1}, {0.9, 50, 2}, {0.78, 40, 4}, {0.6, 20, 7}, {0.3, 10, 6},
	})
	sc := Calibrate(events, testConfig(""), CalibrateFilter{Score: "tier"})[0]
	if sc.Suggested != nil || !sc.SampleShort {
		t.Fatalf("suggested = %v, sampleShort = %v; want nil/true", sc.Suggested, sc.SampleShort)
	}
}

func TestCalibrateHigherEscalatesDirection(t *testing.T) {
	// review scores: high bands are the under-tiered ones, so the walk
	// goes the other way and takes a band's lower bound.
	specs := []bandSpec{{0.98, 50, 40}, {0.9, 50, 30}, {0.78, 50, 15}, {0.6, 50, 5}, {0.2, 50, 2}}
	events := reviewEvents(specs)
	sc := Calibrate(events, testConfig(""), CalibrateFilter{Score: "review"})[0]
	if !sc.HigherEscalates {
		t.Fatal("review should escalate on high scores")
	}
	if sc.Suggested == nil || *sc.Suggested != 0.70 {
		t.Fatalf("suggested = %v, want 0.70", sc.Suggested)
	}
}

// reviewEvents builds turns whose reviewed (unresolved) score is conf,
// with the given number of them labelled under-tiered.
func reviewEvents(bands []bandSpec) []Event {
	var events []Event
	i := 0
	next := func(diss bool) Event {
		i++
		ev := Event{Kind: "decide", Session: "s1", Agent: "main", Arm: "router",
			Harness: "claude-code", TurnID: fmt.Sprintf("t%d", i), Tier: TierFast,
			ClassifiedTier: TierFast, Reason: "R6-adopt", Classifier: "typeset/jev-latest"}
		d := 0.0
		if diss {
			d = 0.9
		}
		ev.Dissatisfied = &d
		return ev
	}
	for _, b := range bands {
		for k := 0; k < b.n; k++ {
			ev := next(false)
			u := b.conf
			events = append(events, ev, Event{Kind: "review", Session: "s1",
				TurnID: ev.TurnID, Unresolved: &u})
			if k < b.under {
				events = append(events, next(true)) // the turn after is dissatisfied
			}
		}
	}
	return events
}

func TestCalibrateSkipsControlAndSubagents(t *testing.T) {
	events := tierEvents([]bandSpec{{0.97, 40, 1}})
	// a control-arm turn and a subagent turn must not count
	c1, c2 := 0.97, 0.97
	events = append(events,
		Event{Kind: "decide", Session: "s1", Agent: "main", Arm: "control", Tier: TierFast,
			ClassifiedTier: TierFast, TierConfidence: &c1, TurnID: "c1"},
		Event{Kind: "decide", Session: "s1", Agent: "Explore", Arm: "router", Tier: TierFast,
			ClassifiedTier: TierFast, TierConfidence: &c2, TurnID: "c2"})
	sc := Calibrate(events, testConfig(""), CalibrateFilter{Score: "tier"})[0]
	total := 0
	for _, b := range sc.Bands {
		total += b.N
	}
	if total != 40 {
		t.Fatalf("samples = %d, want 40 (control and subagent turns excluded)", total)
	}
}

func TestCalibrateLabelsIgnoreOwnScore(t *testing.T) {
	events := tierEvents([]bandSpec{{0.98, 60, 30}, {0.9, 60, 20}, {0.78, 60, 10}, {0.6, 60, 5}, {0.2, 60, 2}})
	for i := range events {
		if events[i].Kind == "decide" {
			u := 0.5 + float64(i%50)/100
			events[i].Unresolved = &u
		}
	}
	base := Calibrate(events, testConfig(""), CalibrateFilter{Score: "review"})[0]
	// add R3-tools reasons on the FOLLOWING turn: the review labels must
	// not move (tool stats are only context, §5.2).
	withTools := make([]Event, len(events))
	copy(withTools, events)
	for i := range withTools {
		if withTools[i].Kind == "decide" {
			withTools[i].Reason = "R3-tools"
		}
	}
	other := Calibrate(withTools, testConfig(""), CalibrateFilter{Score: "review"})[0]
	for i := range base.Bands {
		if base.Bands[i].UnderRate != other.Bands[i].UnderRate {
			t.Fatalf("band %d: review labels moved with R3-tools (%v → %v)",
				i, base.Bands[i].UnderRate, other.Bands[i].UnderRate)
		}
	}
	if (base.Suggested == nil) != (other.Suggested == nil) ||
		(base.Suggested != nil && *base.Suggested != *other.Suggested) {
		t.Fatalf("suggestion moved: %v → %v", base.Suggested, other.Suggested)
	}
}
