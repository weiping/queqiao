package router

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/magpie"
	"github.com/yetone/magpie/internal/wire"
)

// stickyTurn is the router's own in-turn record: within a turn every
// request keeps the tier the turn began with (§6.3's stickiness).
type stickyTurn struct {
	turn int
	tier string
	at   time.Time
}

// Route is what the router decided for a request of the router group:
// the tier, the group to send it to (group/qq-…), why, and whether a hint
// stored by /turn answered.
type Route struct {
	Tier   Tier
	Group  string
	Reason string
	Source string // jev | llm | default | hint | held
	Arm    string // router | control
	Hint   bool
}

// Router decides the router group's requests (SP8: called by queqiaod's
// proxy, which used to be the gateway hook): a tier per new turn, the turn
// keeping it until its next turn begins.
type Router struct {
	deps   *Deps
	mu     sync.Mutex
	sticky map[string]stickyTurn
}

// NewRouter builds the router over its deps.
func NewRouter(deps *Deps) *Router {
	if deps.Sessions == nil {
		deps.Sessions = NewSessions()
	}
	if deps.Hints == nil {
		deps.Hints = NewHints()
	}
	if deps.Log == nil {
		deps.Log = Append
	}
	return &Router{deps: deps, sticky: map[string]stickyTurn{}}
}

// Observe records a request's tool stats and time for its session (R3's
// tool rule and R6's SinceLast read them).
func (r *Router) Observe(h http.Header, req *wire.Request) {
	if req == nil {
		return
	}
	r.deps.Sessions.Observe(wire.SessionOf(h), wire.Tools(req))
}

// Route decides a request of the router group, sent by harness ("codex",
// or "gateway" for any other agent). ok is false when the decided tier has
// no group in the configuration: the request is then left as it is.
func (r *Router) Route(ctx context.Context, h http.Header, body []byte, req *wire.Request, harness string) (Route, bool) {
	if req == nil {
		return Route{}, false
	}
	cfg := r.deps.Config
	session := wire.SessionOf(h)
	words := wire.FirstWords(req)
	key := session + "|" + words
	turn, _ := wire.TurnOf(req)

	// every request is observed: the tool stats and SinceLast the next
	// decision goes by (§6.3)
	r.Observe(h, req)
	r.deps.Sessions.NoteWords(session, words)

	// within the turn: what was decided when it began
	r.mu.Lock()
	st, had := r.sticky[key]
	r.mu.Unlock()
	if had && st.turn == turn && time.Since(st.at) < time.Hour {
		return routeFor(cfg, Route{Tier: Tier(st.tier), Reason: "R-held", Source: "held"})
	}

	// a new turn: a stored hint first (§6.5's (a)–(c)), else decide here
	turnID := CodexTurnID(h, body)
	if hint, ok := r.deps.Hints.Take(HintKey{Session: session, TurnID: turnID, PromptHash: PromptHash(wire.UserText(req))}); ok {
		r.hold(key, turn, string(hint.Tier))
		_ = r.deps.Log(Event{Kind: "hint_consumed", Session: session, Tier: hint.Tier, LatencyMs: 0})
		return routeFor(cfg, Route{Tier: hint.Tier, Reason: "hint", Source: "hint", Hint: true})
	}

	if harness == "" {
		harness = "gateway"
	}
	res := r.deps.Decide(ctx, DecideInput{
		Session: session, Key: key, Harness: harness, Agent: "gateway",
		Prompt: wire.UserText(req), TurnID: turnID, FirstWords: words,
	})
	r.hold(key, turn, string(res.Tier))
	return routeFor(cfg, Route{Tier: res.Tier, Reason: res.Reason, Source: res.Source, Arm: res.Arm})
}

// hold remembers the turn's tier for the requests that follow in it.
func (r *Router) hold(key string, turn int, tier string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sticky[key] = stickyTurn{turn: turn, tier: tier, at: time.Now()}
	if len(r.sticky) > 4096 {
		now := time.Now()
		for k, st := range r.sticky {
			if now.Sub(st.at) > time.Hour {
				delete(r.sticky, k)
			}
		}
	}
}

// routeFor fills in the tier's group; ok is false for a tier the
// configuration has no group for.
func routeFor(cfg Config, rt Route) (Route, bool) {
	tc, ok := cfg.Tiers[rt.Tier]
	if !ok {
		return Route{}, false
	}
	rt.Group = magpie.GroupPrefix + tc.Group
	return rt, true
}

// Note writes an event to the router's log (the proxy's passthroughs).
func (r *Router) Note(ev Event) { _ = r.deps.Log(ev) }
