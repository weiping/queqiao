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
// In shadow review mode it also records whether R3's review condition
// would have escalated (Decision.WouldReview).
func Choose(in PolicyInput, cfg PolicyConfig) Decision {
	d, reviewHit := choose(in, cfg)
	if cfg.ReviewMode == ReviewShadow && reviewHit {
		d.WouldReview = true
	}
	return d
}

// choose is Choose without the shadow decoration, and reports whether the
// R3 review condition was reached and met (SP7 §3.5).
func choose(in PolicyInput, cfg PolicyConfig) (Decision, bool) {
	// Subagent statelessness: only "main"/"gateway" carry session state.
	prev := in.Prev
	if in.Agent != "main" && in.Agent != "gateway" {
		prev = nil
	}

	// R1 fixed agent.
	if tier, ok := cfg.FixedAgents[in.Agent]; ok {
		return Decision{Tier: tier, Reason: "R1-fixed", Next: TurnState{Tier: tier}}, false
	}

	// R2 plan mode.
	if in.PlanMode {
		return Decision{Tier: TierPerformance, Reason: "R2-plan-mode", Next: TurnState{Tier: TierPerformance}}, false
	}

	// R5 usability of the classification (gate, not a decision).
	usable := in.Classified != nil && in.Classified.TierConfidence >= cfg.TierMin

	// the R3 review condition, met or not; shadow mode reports it without
	// acting on it (§3.5)
	reviewHit := prev != nil && in.Review != nil &&
		in.Review.Unresolved >= cfg.ReviewMin && in.Review.Confidence >= cfg.ReviewConfMin

	if prev != nil {
		// R3 escalate: (a) user dissatisfied, (b) at least half of >=3 tool
		// calls failed, (c) the review says the last turn left it unresolved.
		dissatisfied := in.Classified != nil && in.Classified.Dissatisfied >= cfg.DissatisfiedMin
		toolsFailing := in.ToolCalls >= 3 && in.ToolFailures*2 >= in.ToolCalls
		reviewing := cfg.ReviewMode == ReviewAct && reviewHit
		if dissatisfied || toolsFailing || reviewing {
			reason := "R3-review"
			if dissatisfied {
				reason = "R3-escalate"
			} else if toolsFailing {
				reason = "R3-tools"
			}
			tier := tierUp(prev.Tier) // nil/untrusted classification counts as prev+1
			if usable && tierRank(in.Classified.Tier) > tierRank(tier) {
				tier = in.Classified.Tier
			}
			return Decision{
				Tier:   tier,
				Reason: reason,
				Next:   TurnState{Tier: tier, EscalatedLeft: cfg.EscalateTurns},
			}, reviewHit
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
			}, reviewHit
		}
	}

	// R6 hysteresis: the only rule that lowers the tier.
	if usable {
		if prev == nil || tierRank(in.Classified.Tier) >= tierRank(prev.Tier) {
			return Decision{Tier: in.Classified.Tier, Reason: "R6-adopt", Next: TurnState{Tier: in.Classified.Tier}}, reviewHit
		}
		if in.SinceLast >= cfg.CacheTTL || prev.LowerStreak+1 >= 2 {
			return Decision{Tier: in.Classified.Tier, Reason: "R6-lower", Next: TurnState{Tier: in.Classified.Tier}}, reviewHit
		}
		return Decision{
			Tier:   prev.Tier,
			Reason: "R6-keep",
			Next:   TurnState{Tier: prev.Tier, LowerStreak: prev.LowerStreak + 1},
		}, reviewHit
	}

	// R7 carry on failed/unusable classification.
	if prev != nil {
		return Decision{Tier: prev.Tier, Reason: "R7-carry", Next: TurnState{Tier: prev.Tier}}, reviewHit
	}

	// R8 default.
	return Decision{Tier: cfg.DefaultTier, Reason: "R8-default", Next: TurnState{Tier: cfg.DefaultTier}}, reviewHit
}
