package router

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// stickyTurn is the router's own in-turn record: within a turn every
// request keeps the tier the turn began with (§6.3's stickiness, kept here
// because the router-group path never enters ruleFor — SP2 review).
type stickyTurn struct {
	turn int
	tier string
	at   time.Time
}

type hookState struct {
	mu     sync.Mutex
	sticky map[string]stickyTurn
}

// Hook is the gateway callback the router installs: tier-group requests
// are only observed; router-group requests get a tier per new turn, the
// turn keeping it until its next turn begins.
type Hook struct {
	deps *Deps
	st   hookState
}

// NewHook builds the gateway callback over the router's deps.
func NewHook(deps *Deps) *Hook {
	if deps.Sessions == nil {
		deps.Sessions = NewSessions()
	}
	if deps.Log == nil {
		deps.Log = Append
	}
	return &Hook{deps: deps, st: hookState{sticky: map[string]stickyTurn{}}}
}

// GatewayHookFunc matches gateway.RouterHookFunc.
func (h *Hook) GatewayHookFunc(hdr http.Header, body []byte, req *gateway.Request, g provider.Group, ms []provider.Member, agent string) *gateway.RuleHit {
	cfg := h.deps.Config
	if g.ID == cfg.RouterGroup {
		return h.routeGroup(hdr, body, req, g)
	}
	for _, tc := range cfg.Tiers {
		if g.ID == tc.Group {
			// a tier group: only the tool stats and request time are
			// recorded (R6's SinceLast); the member order is untouched
			h.deps.Sessions.Observe(gateway.SessionOf(hdr), req)
			return nil
		}
	}
	return nil // not a group the router manages
}

// routeGroup serves the router group: §6.5's consumption path.
func (h *Hook) routeGroup(hdr http.Header, body []byte, req *gateway.Request, g provider.Group) *gateway.RuleHit {
	if req == nil {
		return nil
	}
	cfg := h.deps.Config
	session := gateway.SessionOf(hdr)
	words := gateway.FirstWords(req)
	key := session + "|" + words
	turn, _ := gateway.TurnOf(req)

	// every request is observed: the tool stats and SinceLast the next
	// decision goes by (§6.3)
	h.deps.Sessions.Observe(session, req)
	h.deps.Sessions.NoteWords(session, words)

	// within the turn: what was decided when it began
	h.st.mu.Lock()
	sticky, had := h.st.sticky[key]
	h.st.mu.Unlock()
	if had && sticky.turn == turn && time.Since(sticky.at) < time.Hour {
		return hitFor(cfg, Tier(sticky.tier), &gateway.RouterHit{Tier: sticky.tier, Reason: "R-held", Source: "held"})
	}

	// a new turn: a stored hint first (§6.5's (a)–(c)), else gateway mode
	turnID := CodexTurnID(hdr, body)
	if hint, ok := h.deps.Hints.Take(HintKey{Session: session, TurnID: turnID, PromptHash: PromptHash(gateway.UserText(req))}); ok {
		h.hold(key, turn, string(hint.Tier))
		_ = h.deps.Log(Event{Kind: "hint_consumed", Session: session, Tier: hint.Tier, LatencyMs: 0})
		return hitFor(cfg, hint.Tier, &gateway.RouterHit{Tier: string(hint.Tier), Reason: "hint", Source: "hint", Hint: true})
	}

	res := h.deps.Decide(context.Background(), DecideInput{
		Session: session, Key: key, Harness: "gateway", Agent: "gateway",
		Prompt: gateway.UserText(req), TurnID: turnID, FirstWords: words,
	})
	h.hold(key, turn, string(res.Tier))
	return hitFor(cfg, res.Tier, &gateway.RouterHit{
		Tier: string(res.Tier), Reason: res.Reason, Source: res.Source, Arm: res.Arm,
	})
}

// hold remembers the turn's tier for the requests that follow in it.
func (h *Hook) hold(key string, turn int, tier string) {
	h.st.mu.Lock()
	defer h.st.mu.Unlock()
	h.st.sticky[key] = stickyTurn{turn: turn, tier: tier, at: time.Now()}
	if len(h.st.sticky) > 4096 {
		now := time.Now()
		for k, st := range h.st.sticky {
			if now.Sub(st.at) > time.Hour {
				delete(h.st.sticky, k)
			}
		}
	}
}

// hitFor wraps a tier in the RuleHit the gateway orders members by: the
// tier group's member goes first (N=1 so the trace's Then logic sees a
// rule-shaped hit).
func hitFor(cfg Config, tier Tier, rh *gateway.RouterHit) *gateway.RuleHit {
	tc, ok := cfg.Tiers[tier]
	if !ok {
		return nil
	}
	return &gateway.RuleHit{N: 1, Use: provider.GroupPrefix + tc.Group, Router: rh}
}
