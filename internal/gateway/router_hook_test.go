package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// The router hook (SP2) fires at the request dispatch for the groups it
// manages, reorders the router group by the hit it returns, leaves tier
// groups' order alone, and never sees groups it does not manage. With no
// hook installed nothing changes at all.
func TestRouterHookAtDispatch(t *testing.T) {
	setup(t, provider.Chat, &fake{reply: sse(
		`data: {"id":"c1","model":"m1","choices":[{"delta":{"role":"assistant","content":"ok"}}]}`,
		`data: {"id":"c1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		`data: [DONE]`)})
	for _, g := range []provider.Group{
		{ID: "queqiao", Name: "Q", Members: []string{"fake/m1"}},
		{ID: "qq-fast", Name: "F", Members: []string{"fake/m1"}},
	} {
		if err := provider.SaveGroup(g); err != nil {
			t.Fatal(err)
		}
	}

	var calls atomic.Int32
	use := ""
	hdrSeen := http.Header{}
	managed := map[string]bool{"queqiao": true, "qq-fast": true}
	SetRouterHook(func(h http.Header, body []byte, req *Request, g provider.Group, ms []provider.Member, agent string) *RuleHit {
		calls.Add(1)
		if g.ID != "queqiao" {
			return nil // tier groups: observe only
		}
		hdrSeen = h.Clone()
		return &RuleHit{N: 1, Use: use, Router: &RouterHit{Tier: "fast", Reason: "R6-adopt", Source: "llm"}}
	}, func(g provider.Group) bool { return managed[g.ID] })
	t.Cleanup(func() { SetRouterHook(nil, nil) })

	body := `{"model":"group/queqiao","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer magpie")
	req.Header.Set("x-claude-code-session-id", "sess-hook")
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("router-group request: %d %s", rec.Code, rec.Body.String())
	}
	if got := calls.Load(); got == 0 {
		t.Fatal("hook never called for the router group")
	}
	if hdrSeen.Get("x-claude-code-session-id") != "sess-hook" {
		t.Fatal("hook did not see the request headers")
	}

	// a tier-group request calls the hook but nothing reorders; the
	// request goes through unchanged
	before := calls.Load()
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
		`{"model":"group/qq-fast","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	req2.Header.Set("Authorization", "Bearer magpie")
	rec2 := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("tier-group request: %d %s", rec2.Code, rec2.Body.String())
	}
	if calls.Load() != before+1 {
		t.Fatalf("tier-group hook calls: %d → %d", before, calls.Load())
	}

	// a group the router does not manage never reaches the hook
	if err := provider.SaveGroup(provider.Group{ID: "plain", Name: "P", Members: []string{"fake/m1"}}); err != nil {
		t.Fatal(err)
	}
	before = calls.Load()
	req3 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
		`{"model":"group/plain","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	req3.Header.Set("Authorization", "Bearer magpie")
	rec3 := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("unmanaged group request: %d", rec3.Code)
	}
	if calls.Load() != before {
		t.Fatalf("unmanaged group reached the hook: %d → %d", before, calls.Load())
	}
}

// MuxRegister's registrations land on the gateway's mux (the router's
// /v1/queqiao endpoints mount this way; the gateway never imports the
// router package).
func TestMuxRegister(t *testing.T) {
	old := MuxRegister
	MuxRegister = append(MuxRegister, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/queqiao/probe", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(204)
		})
	})
	t.Cleanup(func() { MuxRegister = old })

	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/queqiao/probe", nil))
	if rec.Code != 204 {
		t.Fatalf("probe: %d", rec.Code)
	}
}
