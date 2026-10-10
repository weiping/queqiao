package router

import (
	"context"
	"net/http"
	"testing"

	"github.com/yetone/magpie/internal/wire"
)

// hdr builds headers with a Claude-Code-style session id.
func hdr(session string) http.Header {
	h := http.Header{}
	h.Set("x-claude-code-session-id", session)
	return h
}

// irReq builds a parsed IR request: first user message (a new turn).
func irReq(text string) *wire.Request {
	return &wire.Request{
		Messages: []wire.Message{
			{Role: "user", Parts: []wire.Part{{Kind: wire.Text, Text: text}}},
		},
	}
}

// irTurnReq is a within-turn follow-up: tool results under the last user
// message.
func irTurnReq() *wire.Request {
	return &wire.Request{
		Messages: []wire.Message{
			{Role: "user", Parts: []wire.Part{{Kind: wire.Text, Text: "do it"}}},
			{Role: "assistant", Parts: []wire.Part{{Kind: wire.ToolCall}}},
			{Role: "user", Parts: []wire.Part{{Kind: wire.ToolResult}}},
		},
	}
}

// route runs a request of the router group through the Router, as the
// proxy does.
func route(r *Router, h http.Header, req *wire.Request, harness string) Route {
	got, _ := r.Route(context.Background(), h, nil, req, harness)
	return got
}

func TestRouteRouterGroupNewTurn(t *testing.T) {
	d, fc, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewRouter(d)
	hit := route(h, hdr("s1"), irReq("hello"), "claude-code")
	if hit.Group != "group/qq-balanced" || hit.Tier != TierBalanced || hit.Source != "llm" {
		t.Fatalf("hit: %+v", hit)
	}
	if len(fc.questions) != 1 {
		t.Fatalf("classified %d times", len(fc.questions))
	}
	// gateway-mode state was committed under session|firstWords
	if st := d.Sessions.Get("s1|" + wire.FirstWords(irReq("hello"))); st == nil || st.Tier != TierBalanced {
		t.Fatalf("state: %+v", st)
	}
}

func TestRouteInTurnKeepsTierWithoutClassifying(t *testing.T) {
	d, fc, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewRouter(d)
	first := irReq("hello")
	_ = route(h, hdr("s1"), first, "claude-code")
	// the follow-up shares the first message, so the same firstWords key
	follow := &wire.Request{
		Messages: append(append([]wire.Message{}, first.Messages...),
			wire.Message{Role: "assistant", Parts: []wire.Part{{Kind: wire.ToolCall}}},
			wire.Message{Role: "user", Parts: []wire.Part{{Kind: wire.ToolResult}}},
		),
	}
	hit := route(h, hdr("s1"), follow, "claude-code")
	if hit.Group != "group/qq-balanced" || hit.Reason != "R-held" {
		t.Fatalf("in-turn hit: %+v", hit)
	}
	if len(fc.questions) != 1 {
		t.Fatalf("classified again in-turn: %d", len(fc.questions))
	}
}

func TestRouterObserveCountsTools(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	h := NewRouter(d)
	h.Observe(hdr("s1"), irTurnReq())
	calls, failures := d.Sessions.Stats("s1")
	if calls != 1 || failures != 0 {
		t.Fatalf("observed: %d calls %d failures", calls, failures)
	}
}

// A tier missing from the configuration routes nowhere: ok is false and
// the proxy leaves the request as it is.
func TestRouteUnknownTierIsNotOK(t *testing.T) {
	d, _, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9})
	delete(d.Config.Tiers, TierBalanced)
	d.Config.DefaultTier = TierBalanced
	if got, ok := NewRouter(d).Route(context.Background(), hdr("s1"), nil, irReq("x"), "gateway"); ok {
		t.Fatalf("routed to %+v", got)
	}
}

func TestRouteHintConsumed(t *testing.T) {
	d, fc, events := testDeps(t, &Verdict{Tier: TierPerformance, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewRouter(d)
	// the /turn path stored a hint for this session+turn
	req := irReq("fix the bug")
	hash := PromptHash(wire.UserText(req))
	d.Hints.Put(Hint{Key: HintKey{Session: "s1", TurnID: "t-9", PromptHash: hash}, Tier: TierFast})
	hdr := hdr("s1")
	hdr.Set("x-codex-turn-metadata", `{"turn_id":"t-9"}`)
	hit := route(h, hdr, req, "codex")
	if hit.Group != "group/qq-fast" || !hit.Hint {
		t.Fatalf("hint hit: %+v", hit)
	}
	if len(fc.questions) != 0 {
		t.Fatal("hint path classified anyway")
	}
	found := false
	for _, ev := range *events {
		if ev.Kind == "hint_consumed" {
			found = true
		}
	}
	if !found {
		t.Fatal("no hint_consumed event")
	}
}

func TestRouteForkSharesKeyWithParent(t *testing.T) {
	// S11: a fork reuses the parent's session header and first message, so
	// in gateway mode it lands on the same state key — treated as another
	// turn of the main session (§5.8's last note).
	d, _, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewRouter(d)
	_ = route(h, hdr("parent-1"), irReq("remember 42"), "claude-code")
	// the fork: same header, same first message, a second turn of text
	fork := &wire.Request{
		Messages: []wire.Message{
			{Role: "user", Parts: []wire.Part{{Kind: wire.Text, Text: "remember 42"}}},
			{Role: "assistant", Parts: []wire.Part{{Kind: wire.Text, Text: "ok"}}},
			{Role: "user", Parts: []wire.Part{{Kind: wire.Text, Text: "what did I say"}}},
		},
	}
	hit := route(h, hdr("parent-1"), fork, "claude-code")
	if hit.Group == "" {
		t.Fatal("fork not routed")
	}
	// same session|firstWords key: the state carries the parent's turn
	st := d.Sessions.Get("parent-1|" + wire.FirstWords(irReq("remember 42")))
	if st == nil {
		t.Fatal("fork did not share the parent's state key")
	}
}
