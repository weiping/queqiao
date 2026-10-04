package router

import (
	"net/http"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// hdr builds headers with a Claude-Code-style session id.
func hdr(session string) http.Header {
	h := http.Header{}
	h.Set("x-claude-code-session-id", session)
	return h
}

// irReq builds a parsed IR request: first user message (a new turn).
func irReq(text string) *gateway.Request {
	return &gateway.Request{
		Messages: []gateway.Message{
			{Role: "user", Parts: []gateway.Part{{Kind: gateway.Text, Text: text}}},
		},
	}
}

// irTurnReq is a within-turn follow-up: tool results under the last user
// message.
func irTurnReq() *gateway.Request {
	return &gateway.Request{
		Messages: []gateway.Message{
			{Role: "user", Parts: []gateway.Part{{Kind: gateway.Text, Text: "do it"}}},
			{Role: "assistant", Parts: []gateway.Part{{Kind: gateway.ToolCall, Name: "Bash", ID: "c1"}}},
			{Role: "user", Parts: []gateway.Part{{Kind: gateway.ToolResult, CallID: "c1", Text: "done"}}},
		},
	}
}

func routerGroup() provider.Group {
	return provider.Group{ID: "queqiao", Members: []string{
		"group/qq-balanced", "group/qq-perf", "group/qq-fast",
	}}
}

func tierGroup() provider.Group { return provider.Group{ID: "qq-fast"} }

func TestHookRouterGroupNewTurn(t *testing.T) {
	d, fc, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewHook(d)
	hit := h.GatewayHookFunc(hdr("s1"), nil, irReq("hello"), routerGroup(), nil, "claude-code")
	if hit == nil || hit.Use != "group/qq-balanced" || hit.N != 1 {
		t.Fatalf("hit: %+v", hit)
	}
	if hit.Router == nil || hit.Router.Tier != "balanced" || hit.Router.Source != "llm" {
		t.Fatalf("router hit: %+v", hit.Router)
	}
	if len(fc.questions) != 1 {
		t.Fatalf("classified %d times", len(fc.questions))
	}
	// gateway-mode state was committed under session|firstWords
	if st := d.Sessions.Get("s1|" + gateway.FirstWords(irReq("hello"))); st == nil || st.Tier != TierBalanced {
		t.Fatalf("state: %+v", st)
	}
}

func TestHookInTurnKeepsTierWithoutClassifying(t *testing.T) {
	d, fc, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewHook(d)
	first := irReq("hello")
	_ = h.GatewayHookFunc(hdr("s1"), nil, first, routerGroup(), nil, "claude-code")
	// the follow-up shares the first message, so the same firstWords key
	follow := &gateway.Request{
		Messages: append(append([]gateway.Message{}, first.Messages...),
			gateway.Message{Role: "assistant", Parts: []gateway.Part{{Kind: gateway.ToolCall, Name: "Bash", ID: "c1"}}},
			gateway.Message{Role: "user", Parts: []gateway.Part{{Kind: gateway.ToolResult, CallID: "c1", Text: "done"}}},
		),
	}
	hit := h.GatewayHookFunc(hdr("s1"), nil, follow, routerGroup(), nil, "claude-code")
	if hit == nil || hit.Use != "group/qq-balanced" {
		t.Fatalf("in-turn hit: %+v", hit)
	}
	if hit.Router == nil || hit.Router.Reason != "R-held" {
		t.Fatalf("in-turn router hit: %+v", hit.Router)
	}
	if len(fc.questions) != 1 {
		t.Fatalf("classified again in-turn: %d", len(fc.questions))
	}
}

func TestHookTierGroupOnlyObserves(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	h := NewHook(d)
	req := irTurnReq()
	hit := h.GatewayHookFunc(hdr("s1"), nil, req, tierGroup(), nil, "claude-code")
	if hit != nil {
		t.Fatalf("tier group reordered: %+v", hit)
	}
	calls, failures := d.Sessions.Stats("s1")
	if calls != 1 || failures != 0 {
		t.Fatalf("observed: %d calls %d failures", calls, failures)
	}
}

func TestHookUnmanagedGroupIgnored(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	h := NewHook(d)
	if hit := h.GatewayHookFunc(hdr("s1"), nil, irReq("x"), provider.Group{ID: "other"}, nil, "a"); hit != nil {
		t.Fatalf("unmanaged group: %+v", hit)
	}
}

func TestHookHintConsumed(t *testing.T) {
	d, fc, events := testDeps(t, &Verdict{Tier: TierPerformance, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewHook(d)
	// the /turn path stored a hint for this session+turn
	req := irReq("fix the bug")
	hash := PromptHash(gateway.UserText(req))
	d.Hints.Put(Hint{Key: HintKey{Session: "s1", TurnID: "t-9", PromptHash: hash}, Tier: TierFast})
	hdr := hdr("s1")
	hdr.Set("x-codex-turn-metadata", `{"turn_id":"t-9"}`)
	hit := h.GatewayHookFunc(hdr, nil, req, routerGroup(), nil, "codex")
	if hit == nil || hit.Use != "group/qq-fast" || !hit.Router.Hint {
		t.Fatalf("hint hit: %+v %+v", hit, hit.Router)
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

func TestHookForkSharesKeyWithParent(t *testing.T) {
	// S11: a fork reuses the parent's session header and first message, so
	// in gateway mode it lands on the same state key — treated as another
	// turn of the main session (§5.8's last note).
	d, _, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	h := NewHook(d)
	_ = h.GatewayHookFunc(hdr("parent-1"), nil, irReq("remember 42"), routerGroup(), nil, "claude-code")
	// the fork: same header, same first message, a second turn of text
	fork := &gateway.Request{
		Messages: []gateway.Message{
			{Role: "user", Parts: []gateway.Part{{Kind: gateway.Text, Text: "remember 42"}}},
			{Role: "assistant", Parts: []gateway.Part{{Kind: gateway.Text, Text: "ok"}}},
			{Role: "user", Parts: []gateway.Part{{Kind: gateway.Text, Text: "what did I say"}}},
		},
	}
	hit := h.GatewayHookFunc(hdr("parent-1"), nil, fork, routerGroup(), nil, "claude-code")
	if hit == nil {
		t.Fatal("fork not routed")
	}
	// same session|firstWords key: the state carries the parent's turn
	st := d.Sessions.Get("parent-1|" + gateway.FirstWords(irReq("remember 42")))
	if st == nil {
		t.Fatal("fork did not share the parent's state key")
	}
}
