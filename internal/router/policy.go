package router

// tierRank orders tiers: fast < balanced < performance. Unknown tiers rank lowest.
func tierRank(t Tier) int {
	switch t {
	case TierBalanced:
		return 1
	case TierPerformance:
		return 2
	default:
		return 0
	}
}

// tierUp returns the next tier up, capped at performance.
func tierUp(t Tier) Tier {
	switch t {
	case TierFast:
		return TierBalanced
	default:
		return TierPerformance
	}
}

// Choose decides the tier for one turn. It is pure: no I/O, fully
// deterministic. Rules R1–R8 are checked in order, first match wins.
func Choose(in PolicyInput, cfg PolicyConfig) Decision {
	// Subagent statelessness: only "main"/"gateway" carry session state.
	prev := in.Prev
	if in.Agent != "main" && in.Agent != "gateway" {
		prev = nil
	}

	// R1 fixed agent.
	if tier, ok := cfg.FixedAgents[in.Agent]; ok {
		return Decision{Tier: tier, Reason: "R1-fixed", Next: TurnState{Tier: tier}}
	}

	// R2 plan mode.
	if in.PlanMode {
		return Decision{Tier: TierPerformance, Reason: "R2-plan-mode", Next: TurnState{Tier: TierPerformance}}
	}

	// R5 usability of the classification (gate, not a decision).
	usable := in.Classified != nil && in.Classified.TierConfidence >= cfg.TierMin

	if prev != nil {
		// R3 escalate: user dissatisfied, or at least half of >=3 tool calls failed.
		dissatisfied := in.Classified != nil && in.Classified.Dissatisfied >= cfg.DissatisfiedMin
		toolsFailing := in.ToolCalls >= 3 && in.ToolFailures*2 >= in.ToolCalls
		if dissatisfied || toolsFailing {
			tier := tierUp(prev.Tier) // nil/untrusted classification counts as prev+1
			if usable && tierRank(in.Classified.Tier) > tierRank(tier) {
				tier = in.Classified.Tier
			}
			return Decision{
				Tier:   tier,
				Reason: "R3-escalate",
				Next:   TurnState{Tier: tier, EscalatedLeft: cfg.EscalateTurns},
			}
		}

		// R4 escalation hold.
		if prev.EscalatedLeft > 0 {
			tier := prev.Tier // nil/untrusted classification counts as the lowest tier
			if usable && tierRank(in.Classified.Tier) > tierRank(tier) {
				tier = in.Classified.Tier
			}
			return Decision{
				Tier:   tier,
				Reason: "R4-escalation-hold",
				Next:   TurnState{Tier: tier, EscalatedLeft: prev.EscalatedLeft - 1},
			}
		}
	}

	// R6 hysteresis: the only rule that lowers the tier.
	if usable {
		if prev == nil || tierRank(in.Classified.Tier) >= tierRank(prev.Tier) {
			return Decision{Tier: in.Classified.Tier, Reason: "R6-adopt", Next: TurnState{Tier: in.Classified.Tier}}
		}
		if in.SinceLast >= cfg.CacheTTL || prev.LowerStreak+1 >= 2 {
			return Decision{Tier: in.Classified.Tier, Reason: "R6-lower", Next: TurnState{Tier: in.Classified.Tier}}
		}
		return Decision{
			Tier:   prev.Tier,
			Reason: "R6-keep",
			Next:   TurnState{Tier: prev.Tier, LowerStreak: prev.LowerStreak + 1},
		}
	}

	// R7 carry on failed/unusable classification.
	if prev != nil {
		return Decision{Tier: prev.Tier, Reason: "R7-carry", Next: TurnState{Tier: prev.Tier}}
	}

	// R8 default.
	return Decision{Tier: cfg.DefaultTier, Reason: "R8-default", Next: TurnState{Tier: cfg.DefaultTier}}
}
