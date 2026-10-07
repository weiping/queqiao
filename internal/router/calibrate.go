package router

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

/**
 * Score calibration (SP7 §5): how well each score separates turns the user
 * accepted from turns they had to have redone. The labels are automatic
 * and biased — they only see dissatisfaction the user expressed — hence
 * the fixed note at the top of the CLI output.
 */

// calBands are the five fixed segments, high to low (§5.3).
var calBands = [][2]float64{{0.95, 1.00}, {0.85, 0.95}, {0.70, 0.85}, {0.50, 0.70}, {0, 0.50}}

// calMinSamples is the per-band floor: below it a score gets no
// suggestion (§5.3).
const calMinSamples = 30

// underDissatisfied is the fixed next-turn dissatisfaction label (0.5, not
// the configured threshold, so the label does not move with the thing it
// calibrates, §5.2).
const underDissatisfied = 0.5

// CalibrateFilter narrows the turns a calibration looks at.
type CalibrateFilter struct {
	Since   time.Time // zero means all
	Score   string    // "" = all, else tier | dissatisfied | review
	Harness string    // "" = all
	Agent   string    // "" = main; "sub" = every non-main agent
}

// CalibrationBand is one segment's share of a score and how often that
// segment's turns turned out under-tiered.
type CalibrationBand struct {
	Lo, Hi    float64
	N         int
	Share     float64 // of the score's samples
	UnderRate float64
}

// ScoreCalibration is one score's table plus its suggested threshold and
// the trade-off that threshold buys.
type ScoreCalibration struct {
	Score             string // tier | dissatisfied | review
	HigherEscalates   bool   // true when a HIGH score should escalate
	Bands             []CalibrationBand
	Current           float64
	Suggested         *float64
	SampleShort       bool
	EscalateRate      float64 // share of samples the current threshold escalates
	UnderRateKept     float64 // under-tier rate among turns it does not escalate
	ProjEscalateRate  float64
	ProjUnderRateKept float64
}

// calTurn is one routed turn with everything calibration reads from it.
type calTurn struct {
	session, harness, agent, turnID string
	tier                            Tier
	classifiedTier                  Tier
	tierConf                        *float64
	dissatisfied                    *float64
	unresolved                      *float64
	reason                          string
	order                           int
	switchedHigher                  bool
	nextDissatisfied                bool
	nextTools                       bool
}

func (t *calTurn) label(score string) bool {
	switch score {
	case "tier":
		return t.nextDissatisfied || t.switchedHigher || t.nextTools
	case "dissatisfied":
		return t.switchedHigher || (t.reason == "R3-escalate" && t.nextDissatisfied)
	default: // review, unresolved
		return t.nextDissatisfied || t.switchedHigher
	}
}

func (t *calTurn) value(score string) (float64, bool) {
	switch score {
	case "tier":
		if t.tierConf == nil || t.classifiedTier == "" || t.tier != t.classifiedTier {
			return 0, false // only turns that adopted the classifier's pick
		}
		return *t.tierConf, true
	case "dissatisfied":
		if t.dissatisfied == nil {
			return 0, false // the first turn has nothing to be dissatisfied with
		}
		return *t.dissatisfied, true
	default:
		if t.unresolved == nil {
			return 0, false
		}
		return *t.unresolved, true
	}
}

// Calibrate builds the score tables (§5.2, §5.3). It is pure: the caller
// reads router.jsonl and the config.
func Calibrate(events []Event, cfg Config, f CalibrateFilter) []ScoreCalibration {
	scores := []string{"tier", "dissatisfied", "review"}
	if f.Score != "" {
		scores = []string{f.Score}
	}
	turns := calTurns(events, cfg, f)
	out := make([]ScoreCalibration, 0, len(scores))
	for _, s := range scores {
		out = append(out, calibrateScore(s, turns, cfg))
	}
	return out
}

// calTurns walks the events once and returns the qualifying turns in
// order, with each turn's under-tier label already resolved.
func calTurns(events []Event, cfg Config, f CalibrateFilter) []*calTurn {
	agent := f.Agent
	if agent == "" {
		agent = "main"
	}
	matches := func(ev Event) bool {
		if f.Harness != "" && ev.Harness != f.Harness {
			return false
		}
		if agent == "main" {
			return ev.Agent == "main"
		}
		return ev.Agent != "main" && ev.Agent != ""
	}
	bySession := map[string][]*calTurn{}
	// switches are the manual model picks, kept per session with the event
	// position they fall at.
	type switchMark struct {
		order int
		tier  Tier
	}
	switches := map[string][]switchMark{}
	var order int
	for _, ev := range events {
		if !f.Since.IsZero() {
			if at, err := time.Parse(time.RFC3339, ev.Time); err == nil && at.Before(f.Since) {
				continue
			}
		}
		order++
		switch ev.Kind {
		case "review":
			for _, t := range bySession[ev.Session] {
				if ev.TurnID != "" && t.turnID == ev.TurnID && t.unresolved == nil {
					t.unresolved = ev.Unresolved
				}
			}
		case "decide":
			if ev.Arm == "control" || !matches(ev) {
				continue
			}
			t := &calTurn{session: ev.Session, harness: ev.Harness, agent: ev.Agent,
				turnID: ev.TurnID, tier: ev.Tier, classifiedTier: ev.ClassifiedTier,
				tierConf: ev.TierConfidence, dissatisfied: ev.Dissatisfied,
				reason: ev.Reason, order: order}
			bySession[ev.Session] = append(bySession[ev.Session], t)
		case "feedback":
			if !strings.HasPrefix(ev.Extra, "manual_model_switch") {
				continue
			}
			if to, ok := tierFromExtra(ev.Extra); ok {
				switches[ev.Session] = append(switches[ev.Session], switchMark{order: order, tier: to})
			}
		}
	}
	// A manual switch counts for the turn it follows: switching to a higher
	// tier than that turn ran on says the turn was under-tiered. A switch
	// with no tier in its text is ignored.
	for session, marks := range switches {
		ts := bySession[session]
		for _, m := range marks {
			for i := len(ts) - 1; i >= 0; i-- {
				if ts[i].order < m.order {
					if tierRank(m.tier) > tierRank(ts[i].tier) {
						ts[i].switchedHigher = true
					}
					break
				}
			}
		}
	}

	var all []*calTurn
	for _, ts := range bySession {
		for i, t := range ts {
			if i+1 < len(ts) {
				next := ts[i+1]
				if next.dissatisfied != nil && *next.dissatisfied >= underDissatisfied {
					t.nextDissatisfied = true
				}
				t.nextTools = next.reason == "R3-tools"
			}
			all = append(all, t)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].order < all[j].order })
	return all
}

// tierFromExtra reads the tier out of a feedback event's "kind tier" text.
func tierFromExtra(extra string) (Tier, bool) {
	fields := strings.Fields(extra)
	if len(fields) < 2 {
		return "", false
	}
	t := Tier(fields[len(fields)-1])
	switch t {
	case TierFast, TierBalanced, TierPerformance:
		return t, true
	}
	return "", false
}

// calibrateScore builds one score's bands, suggestion and projection.
func calibrateScore(score string, turns []*calTurn, cfg Config) ScoreCalibration {
	higher := score != "tier"
	out := ScoreCalibration{Score: score, HigherEscalates: higher, Current: currentThreshold(score, cfg)}
	out.Bands = make([]CalibrationBand, len(calBands))
	values := make([]float64, 0, len(turns))
	under := make([]bool, 0, len(turns))
	for _, t := range turns {
		v, ok := t.value(score)
		if !ok {
			continue
		}
		values = append(values, v)
		under = append(under, t.label(score))
	}
	counts := make([]int, len(calBands))
	unders := make([]int, len(calBands))
	for i, v := range values {
		b := bandOf(v)
		counts[b]++
		if under[i] {
			unders[b]++
		}
	}
	out.SampleShort = false
	total := len(values)
	for i, seg := range calBands {
		out.Bands[i] = CalibrationBand{Lo: seg[0], Hi: seg[1], N: counts[i]}
		if total > 0 {
			out.Bands[i].Share = float64(counts[i]) / float64(total)
		}
		if counts[i] > 0 {
			out.Bands[i].UnderRate = float64(unders[i]) / float64(counts[i])
		}
		if counts[i] < calMinSamples {
			out.SampleShort = true
		}
	}
	if total > 0 && !out.SampleShort {
		out.Suggested = suggest(score, out.Bands)
	}
	out.EscalateRate, out.UnderRateKept = escalateAndKeep(values, under, out.Current, higher)
	if out.Suggested != nil {
		out.ProjEscalateRate, out.ProjUnderRateKept = escalateAndKeep(values, under, *out.Suggested, higher)
	}
	return out
}

// bandOf indexes calBands by value, 0 = the highest segment.
func bandOf(v float64) int {
	for i, seg := range calBands {
		if v >= seg[0] {
			return i
		}
	}
	return len(calBands) - 1
}

// suggest walks the bands the way §5.3 says: for a score where low means
// escalate, from the top down, taking the upper bound of the first band
// whose under-rate is both twice the rate of the bands above it and 10
// points higher; for scores where high means escalate, from the bottom up
// against the bands below.
func suggest(score string, bands []CalibrationBand) *float64 {
	higher := score != "tier"
	if !higher {
		for i := 0; i < len(bands); i++ {
			aboveN, aboveUnder := 0, 0
			for j := 0; j < i; j++ {
				aboveN += bands[j].N
				aboveUnder += int(bands[j].UnderRate * float64(bands[j].N))
			}
			if aboveN == 0 {
				continue
			}
			above := float64(aboveUnder) / float64(aboveN)
			if bands[i].UnderRate >= 2*above && bands[i].UnderRate-above >= 0.10 {
				hi := bands[i].Hi
				return &hi
			}
		}
		return nil
	}
	for i := len(bands) - 1; i >= 0; i-- {
		belowN, belowUnder := 0, 0
		for j := i + 1; j < len(bands); j++ {
			belowN += bands[j].N
			belowUnder += int(bands[j].UnderRate * float64(bands[j].N))
		}
		if belowN == 0 {
			continue
		}
		below := float64(belowUnder) / float64(belowN)
		if bands[i].UnderRate >= 2*below && bands[i].UnderRate-below >= 0.10 {
			lo := bands[i].Lo
			return &lo
		}
	}
	return nil
}

// escalateAndKeep reports the escalation rate at a threshold and, among
// the turns it does not escalate, the under-tier rate.
func escalateAndKeep(values []float64, under []bool, threshold float64, higher bool) (rate, keptRate float64) {
	if len(values) == 0 {
		return 0, 0
	}
	escalated, kept, keptUnder := 0, 0, 0
	for i, v := range values {
		if (higher && v >= threshold) || (!higher && v < threshold) {
			escalated++
			continue
		}
		kept++
		if under[i] {
			keptUnder++
		}
	}
	rate = float64(escalated) / float64(len(values))
	if kept > 0 {
		keptRate = float64(keptUnder) / float64(kept)
	}
	return rate, keptRate
}

// currentThreshold is the configured cut-off each score uses today.
func currentThreshold(score string, cfg Config) float64 {
	switch score {
	case "tier":
		return cfg.Thresholds.TierMin
	case "dissatisfied":
		return cfg.Thresholds.DissatisfiedMin
	default:
		return cfg.Thresholds.ReviewMin
	}
}

// escapeCSV quotes a CSV field.
func escapeCSV(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// formatScore prints a score the way the tables do.
func formatScore(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }
