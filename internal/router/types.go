package router

import "time"

type Tier string // "fast" | "balanced" | "performance"

const (
	TierFast        Tier = "fast"
	TierBalanced    Tier = "balanced"
	TierPerformance Tier = "performance"
)

type Verdict struct {
	Tier           Tier
	TierConfidence float64 // 0–1
	Dissatisfied   float64 // noul 0–1
	// Source names the model that gave the scores, with "#plain" when the
	// fallback prompt was used (SP7 §4), e.g. "typesafe/jev-latest" or
	// "deepseek/deepseek-flash#plain".
	Source string
}

type TurnState struct {
	Tier          Tier
	EscalatedLeft int // turns of escalation left
	LowerStreak   int // consecutive rounds classified below current tier
}

type PolicyInput struct {
	Agent        string // "main" | "gateway" | subagent type
	PlanMode     bool
	Classified   *Verdict   // nil = classify failed/timed out
	Prev         *TurnState // nil = first round
	ToolFailures int
	ToolCalls    int
	SinceLast    time.Duration
	Now          time.Time
	Review       *ReviewVerdict // last turn's end-of-turn review; nil when none (SP7)
}

// ReviewVerdict is the end-of-turn review's reading of the previous turn
// (SP7 §3.4): how unresolved it left the request, and how sure the
// classifier is of that.
type ReviewVerdict struct {
	Unresolved float64 // 0–1, higher = less resolved
	Confidence float64 // 0–1
}

type PolicyConfig struct {
	FixedAgents     map[string]Tier
	DefaultTier     Tier
	TierMin         float64
	DissatisfiedMin float64
	ReviewMin       float64
	ReviewConfMin   float64
	ReviewMode      string
	EscalateTurns   int
	CacheTTL        time.Duration
}

type Decision struct {
	Tier   Tier
	Reason string // e.g. "R1-fixed"
	Next   TurnState
	// WouldReview is the shadow review mode's record: the R3 review
	// condition would have escalated, but the mode is shadow, so the tier
	// is whatever the other rules chose (SP7).
	WouldReview bool
}
