package router

import (
	"testing"
	"time"
)

var testCfg = PolicyConfig{
	FixedAgents:     map[string]Tier{"Explore": TierFast},
	DefaultTier:     TierBalanced,
	TierMin:         0.5,
	DissatisfiedMin: 0.5,
	EscalateTurns:   2,
	CacheTTL:        time.Hour,
}

func verdict(tier Tier, confidence, dissatisfied float64) *Verdict {
	return &Verdict{Tier: tier, TierConfidence: confidence, Dissatisfied: dissatisfied}
}

func state(tier Tier, escalatedLeft, lowerStreak int) *TurnState {
	return &TurnState{Tier: tier, EscalatedLeft: escalatedLeft, LowerStreak: lowerStreak}
}

func assertDecision(t *testing.T, d Decision, tier Tier, reason string, next TurnState) {
	t.Helper()
	if d.Tier != tier || d.Reason != reason || d.Next != next {
		t.Fatalf("got %+v, want tier=%s reason=%s next=%+v", d, tier, reason, next)
	}
}

func TestR1FixedAgent(t *testing.T) {
	cfg := PolicyConfig{FixedAgents: map[string]Tier{"Explore": TierFast}, DefaultTier: TierBalanced}
	d := Choose(PolicyInput{Agent: "Explore", Now: time.Now()}, cfg)
	if d.Tier != TierFast || d.Reason != "R1-fixed" {
		t.Fatalf("%+v", d)
	}
}

func TestR1FixedAgentTable(t *testing.T) {
	cfg := PolicyConfig{
		FixedAgents: map[string]Tier{"Explore": TierFast, "reviewer": TierPerformance},
		DefaultTier: TierBalanced,
		TierMin:     0.5,
	}
	tests := []struct {
		name  string
		agent string
		want  Tier
	}{
		{"fast mapping wins over everything", "Explore", TierFast},
		{"performance mapping", "reviewer", TierPerformance},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fixed tier wins even with a higher classification and plan mode off.
			d := Choose(PolicyInput{
				Agent:      tt.agent,
				Classified: verdict(TierPerformance, 1, 0),
				Prev:       state(TierPerformance, 0, 0),
				Now:        time.Now(),
			}, cfg)
			assertDecision(t, d, tt.want, "R1-fixed", TurnState{Tier: tt.want})
		})
	}
}

func TestR2PlanMode(t *testing.T) {
	d := Choose(PolicyInput{
		Agent:      "main",
		PlanMode:   true,
		Classified: verdict(TierFast, 1, 0),
		Prev:       state(TierFast, 0, 0),
		Now:        time.Now(),
	}, testCfg)
	assertDecision(t, d, TierPerformance, "R2-plan-mode", TurnState{Tier: TierPerformance})
}

func TestR3EscalateOnDissatisfied(t *testing.T) {
	tests := []struct {
		name string
		in   PolicyInput
		tier Tier
	}{
		{
			name: "untrusted classification escalates to prev+1",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierFast, 0.1, 0.9), // confidence below TierMin
				Prev:       state(TierFast, 0, 0),
				Now:        time.Now(),
			},
			tier: TierBalanced,
		},
		{
			name: "nil classification cannot satisfy dissatisfied branch",
			in: PolicyInput{
				Agent: "main",
				Prev:  state(TierBalanced, 0, 0),
				Now:   time.Now(),
			},
			tier: TierBalanced, // no R3 trigger, falls to R7 carry
		},
		{
			name: "trusted higher classification wins over prev+1",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierPerformance, 0.9, 0.9),
				Prev:       state(TierFast, 0, 0),
				Now:        time.Now(),
			},
			tier: TierPerformance,
		},
		{
			name: "caps at performance",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierFast, 0.9, 0.9),
				Prev:       state(TierPerformance, 0, 0),
				Now:        time.Now(),
			},
			tier: TierPerformance,
		},
		{
			name: "dissatisfied below threshold does not escalate",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierFast, 0.9, 0.4),
				Prev:       state(TierFast, 0, 0),
				Now:        time.Now(),
			},
			tier: TierFast, // R6-adopt
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Choose(tt.in, testCfg)
			if d.Tier != tt.tier {
				t.Fatalf("got %+v, want tier %s", d, tt.tier)
			}
			if tt.name == "untrusted classification escalates to prev+1" ||
				tt.name == "trusted higher classification wins over prev+1" ||
				tt.name == "caps at performance" {
				if d.Reason != "R3-escalate" || d.Next.EscalatedLeft != testCfg.EscalateTurns {
					t.Fatalf("got %+v, want R3-escalate with EscalatedLeft=%d", d, testCfg.EscalateTurns)
				}
			}
		})
	}
}

func TestR3EscalateOnToolFailures(t *testing.T) {
	tests := []struct {
		name       string
		calls      int
		failures   int
		wantTier   Tier
		wantReason string
	}{
		{"half failures with enough calls escalates", 4, 2, TierPerformance, "R3-tools"},
		{"fewer than 3 calls does not escalate", 2, 2, TierBalanced, "R7-carry"},
		{"minority failures does not escalate", 5, 2, TierBalanced, "R7-carry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Choose(PolicyInput{
				Agent:        "main",
				Prev:         state(TierBalanced, 0, 0),
				ToolCalls:    tt.calls,
				ToolFailures: tt.failures,
				Now:          time.Now(),
			}, testCfg)
			wantNext := TurnState{Tier: tt.wantTier}
			if tt.wantReason == "R3-tools" {
				wantNext.EscalatedLeft = testCfg.EscalateTurns
			}
			assertDecision(t, d, tt.wantTier, tt.wantReason, wantNext)
		})
	}
}

func TestR4EscalationHold(t *testing.T) {
	tests := []struct {
		name string
		in   PolicyInput
		tier Tier
	}{
		{
			name: "holds prev tier with untrusted classification",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierFast, 0.1, 0),
				Prev:       state(TierPerformance, 2, 0),
				Now:        time.Now(),
			},
			tier: TierPerformance,
		},
		{
			name: "holds prev tier with nil classification",
			in: PolicyInput{
				Agent: "main",
				Prev:  state(TierPerformance, 1, 0),
				Now:   time.Now(),
			},
			tier: TierPerformance,
		},
		{
			name: "trusted higher classification raises hold",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierPerformance, 0.9, 0),
				Prev:       state(TierBalanced, 2, 0),
				Now:        time.Now(),
			},
			tier: TierPerformance,
		},
		{
			name: "trusted lower classification cannot lower during hold",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierFast, 0.9, 0),
				Prev:       state(TierPerformance, 2, 0),
				SinceLast:  2 * time.Hour, // cold cache still cannot lower during hold
				Now:        time.Now(),
			},
			tier: TierPerformance,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Choose(tt.in, testCfg)
			if d.Tier != tt.tier || d.Reason != "R4-escalation-hold" {
				t.Fatalf("got %+v, want tier=%s reason=R4-escalation-hold", d, tt.tier)
			}
			if d.Next.EscalatedLeft != tt.in.Prev.EscalatedLeft-1 {
				t.Fatalf("got EscalatedLeft=%d, want %d", d.Next.EscalatedLeft, tt.in.Prev.EscalatedLeft-1)
			}
		})
	}
}

func TestR6Adopt(t *testing.T) {
	tests := []struct {
		name string
		in   PolicyInput
		tier Tier
	}{
		{
			name: "first round adopts classified tier",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierFast, 0.9, 0),
				Now:        time.Now(),
			},
			tier: TierFast,
		},
		{
			name: "classified above prev adopts immediately",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierPerformance, 0.9, 0),
				Prev:       state(TierFast, 0, 1),
				Now:        time.Now(),
			},
			tier: TierPerformance,
		},
		{
			name: "classified equal to prev adopts and resets streak",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierBalanced, 0.9, 0),
				Prev:       state(TierBalanced, 0, 1),
				Now:        time.Now(),
			},
			tier: TierBalanced,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Choose(tt.in, testCfg)
			assertDecision(t, d, tt.tier, "R6-adopt", TurnState{Tier: tt.tier})
		})
	}
}

func TestR6HysteresisKeepsWarmCache(t *testing.T) {
	d := Choose(PolicyInput{
		Agent:      "main",
		Classified: verdict(TierBalanced, 0.9, 0),
		Prev:       state(TierPerformance, 0, 0),
		SinceLast:  10 * time.Minute, // warm
		Now:        time.Now(),
	}, testCfg)
	assertDecision(t, d, TierPerformance, "R6-keep", TurnState{Tier: TierPerformance, LowerStreak: 1})
}

func TestR6LowersWhenCacheCold(t *testing.T) {
	d := Choose(PolicyInput{
		Agent:      "main",
		Classified: verdict(TierBalanced, 0.9, 0),
		Prev:       state(TierPerformance, 0, 0),
		SinceLast:  2 * time.Hour, // >= CacheTTL: cold
		Now:        time.Now(),
	}, testCfg)
	assertDecision(t, d, TierBalanced, "R6-lower", TurnState{Tier: TierBalanced})
}

func TestR6LowersAfterTwoConsecutiveLowRounds(t *testing.T) {
	d := Choose(PolicyInput{
		Agent:      "main",
		Classified: verdict(TierBalanced, 0.9, 0),
		Prev:       state(TierPerformance, 0, 1), // one prior low round kept warm
		SinceLast:  10 * time.Minute,
		Now:        time.Now(),
	}, testCfg)
	assertDecision(t, d, TierBalanced, "R6-lower", TurnState{Tier: TierBalanced})
}

func TestR7Carry(t *testing.T) {
	tests := []struct {
		name string
		in   PolicyInput
	}{
		{
			name: "nil classification carries prev tier",
			in: PolicyInput{
				Agent: "main",
				Prev:  state(TierFast, 0, 0),
				Now:   time.Now(),
			},
		},
		{
			name: "untrusted classification carries prev tier",
			in: PolicyInput{
				Agent:      "main",
				Classified: verdict(TierPerformance, 0.4, 0),
				Prev:       state(TierFast, 0, 0),
				Now:        time.Now(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Choose(tt.in, testCfg)
			assertDecision(t, d, TierFast, "R7-carry", TurnState{Tier: TierFast})
		})
	}
}

func TestR8Default(t *testing.T) {
	tests := []struct {
		name string
		in   PolicyInput
	}{
		{"first round without classification", PolicyInput{Agent: "main", Now: time.Now()}},
		{
			"first round with untrusted classification",
			PolicyInput{Agent: "main", Classified: verdict(TierFast, 0.1, 0), Now: time.Now()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Choose(tt.in, testCfg)
			assertDecision(t, d, TierBalanced, "R8-default", TurnState{Tier: TierBalanced})
		})
	}
}

func TestSubagentStatelessness(t *testing.T) {
	t.Run("ignores escalation hold and defaults", func(t *testing.T) {
		d := Choose(PolicyInput{
			Agent: "Explore-sub", // not fixed in testCfg, not main/gateway
			Prev:  state(TierPerformance, 2, 0),
			Now:   time.Now(),
		}, testCfg)
		// With Prev honored this would be R4-escalation-hold at performance.
		assertDecision(t, d, TierBalanced, "R8-default", TurnState{Tier: TierBalanced})
	})

	t.Run("does not escalate on dissatisfied", func(t *testing.T) {
		d := Choose(PolicyInput{
			Agent:      "coder",
			Classified: verdict(TierFast, 0.9, 0.9),
			Prev:       state(TierFast, 0, 0),
			Now:        time.Now(),
		}, testCfg)
		// With Prev honored this would be R3-escalate; stateless adopts instead.
		assertDecision(t, d, TierFast, "R6-adopt", TurnState{Tier: TierFast})
	})

	t.Run("does not carry prev on classify failure", func(t *testing.T) {
		d := Choose(PolicyInput{
			Agent: "coder",
			Prev:  state(TierPerformance, 0, 0),
			Now:   time.Now(),
		}, testCfg)
		assertDecision(t, d, TierBalanced, "R8-default", TurnState{Tier: TierBalanced})
	})

	t.Run("fixed agent mapping still applies to subagents", func(t *testing.T) {
		d := Choose(PolicyInput{
			Agent: "Explore",
			Prev:  state(TierPerformance, 0, 0),
			Now:   time.Now(),
		}, testCfg)
		assertDecision(t, d, TierFast, "R1-fixed", TurnState{Tier: TierFast})
	})
}

// SP7: the three R3 triggers and their reasons, and shadow.
func TestR3Sources(t *testing.T) {
	prev := &TurnState{Tier: TierFast}
	cfg := PolicyConfig{
		DefaultTier: TierBalanced, TierMin: 0.4, DissatisfiedMin: 0.7,
		ReviewMin: 0.7, ReviewConfMin: 0.5, EscalateTurns: 2,
	}
	fast := &Verdict{Tier: TierFast, TierConfidence: 0.9}
	cases := []struct {
		name   string
		in     PolicyInput
		mode   string
		want   Tier
		reason string
		review bool
	}{
		{"tools failing only", PolicyInput{Agent: "main", Prev: prev, Classified: fast, ToolCalls: 4, ToolFailures: 2}, "act", TierBalanced, "R3-tools", false},
		{"review hits", PolicyInput{Agent: "main", Prev: prev, Classified: fast, Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.6}}, "act", TierBalanced, "R3-review", false},
		{"review confidence too low", PolicyInput{Agent: "main", Prev: prev, Classified: fast, Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.4}}, "act", TierFast, "R6-adopt", false},
		{"shadow does not escalate", PolicyInput{Agent: "main", Prev: prev, Classified: fast, Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.6}}, "shadow", TierFast, "R6-adopt", true},
		{"off never reviews", PolicyInput{Agent: "main", Prev: prev, Classified: fast, Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.6}}, "off", TierFast, "R6-adopt", false},
		{"dissatisfied wins over review", PolicyInput{Agent: "main", Prev: prev, Classified: &Verdict{Tier: TierFast, TierConfidence: 0.9, Dissatisfied: 0.9}, Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.6}}, "act", TierBalanced, "R3-escalate", false},
		{"first turn never reviews", PolicyInput{Agent: "main", Classified: fast, Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.6}}, "act", TierFast, "R6-adopt", false},
		{"capped at performance", PolicyInput{Agent: "main", Prev: &TurnState{Tier: TierPerformance}, Classified: &Verdict{Tier: TierPerformance, TierConfidence: 0.9}, Review: &ReviewVerdict{Unresolved: 0.9, Confidence: 0.9}}, "act", TierPerformance, "R3-review", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			c.ReviewMode = tc.mode
			d := Choose(tc.in, c)
			if d.Tier != tc.want || d.Reason != tc.reason || d.WouldReview != tc.review {
				t.Fatalf("got tier=%s reason=%s wouldReview=%v; want tier=%s reason=%s wouldReview=%v",
					d.Tier, d.Reason, d.WouldReview, tc.want, tc.reason, tc.review)
			}
		})
	}
}

// R3's escalation bookkeeping is the same for every source.
func TestR3SourcesKeepEscalateTurns(t *testing.T) {
	prev := &TurnState{Tier: TierFast}
	cfg := PolicyConfig{DefaultTier: TierBalanced, TierMin: 0.4, DissatisfiedMin: 0.7,
		ReviewMin: 0.7, ReviewConfMin: 0.5, EscalateTurns: 2, ReviewMode: "act"}
	d := Choose(PolicyInput{Agent: "main", Prev: prev, Classified: &Verdict{Tier: TierFast, TierConfidence: 0.9},
		Review: &ReviewVerdict{Unresolved: 0.8, Confidence: 0.6}}, cfg)
	if d.Next.EscalatedLeft != 2 {
		t.Fatalf("EscalatedLeft = %d, want 2", d.Next.EscalatedLeft)
	}
}
