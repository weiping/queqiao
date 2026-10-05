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
}

type PolicyConfig struct {
	FixedAgents     map[string]Tier
	DefaultTier     Tier
	TierMin         float64
	DissatisfiedMin float64
	EscalateTurns   int
	CacheTTL        time.Duration
}

type Decision struct {
	Tier   Tier
	Reason string // e.g. "R1-fixed"
	Next   TurnState
}
