//go:build e2e

package gateway_test

// §8's end-to-end case, Claude Code half: the mod's calling pattern (POST
// /v1/queqiao/turn, then an Anthropic Messages request carrying the
// returned group and the session header) over a gateway with fake tier
// upstreams and a scripted classifier. Three turns of §8's script — a
// simple question, a "不对" turn, a carry-on turn — land on fast,
// balanced (R3 escalates on dissatisfaction), balanced (R4 holds).
//
// External test package: the internal gateway tests cannot import the
// router (cycle), and this test wires the two exactly as router_wiring.go
// does. The classifier is scripted at the ask callback (a fake Jev), not
// over HTTP: the System One wire path is covered by the classify tests.
// Run with `go test -tags nogui,e2e ./internal/gateway -run E2ECC`.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
)

// e2eUp is a fake OpenAI-compatible upstream that records its calls.
type e2eUp struct {
	mu    sync.Mutex
	calls int
}

func (u *e2eUp) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []string{
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
		`data: [DONE]`,
	} {
		io.WriteString(w, chunk+"\n\n")
	}
}

func (u *e2eUp) n() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func TestE2EClaudeCodePattern(t *testing.T) {
	// a clean config home, as the gateway tests' fresh() arranges
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	// three fake upstreams, one per tier, each recording its calls
	ups := map[string]*e2eUp{}
	for _, x := range []struct{ id, model string }{
		{"a", "fastm"}, {"b", "balm"}, {"c", "perfm"},
	} {
		u := &e2eUp{}
		ups[x.id] = u
		srv := httptest.NewServer(u)
		t.Cleanup(srv.Close)
		if err := provider.Save(provider.Provider{ID: x.id, Name: strings.ToUpper(x.id), Key: "k", Models: []string{x.model}, Chat: srv.URL + "/v1"}); err != nil {
			t.Fatal(err)
		}
		if err := catalog.SaveLive(x.id, srv.URL+"/v1", []catalog.Model{{ID: x.model, Context: 200000}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []provider.Group{
		{ID: "qq-fast", Name: "F", Members: []string{"a/fastm"}, Routing: provider.Ordered},
		{ID: "qq-balanced", Name: "B", Members: []string{"b/balm"}, Routing: provider.Ordered},
		{ID: "qq-perf", Name: "P", Members: []string{"c/perfm"}, Routing: provider.Ordered},
		{ID: "queqiao", Name: "Q", Routing: provider.Ordered, Members: []string{"group/qq-balanced", "group/qq-perf", "group/qq-fast"}},
	} {
		if err := provider.SaveGroup(g); err != nil {
			t.Fatal(err)
		}
	}

	// router.json exactly as `queqiao router init` writes it
	routerJSON := filepath.Join(home, ".config", "queqiao", "router.json")
	const criteria = `{
  "version": 1,
  "router_group": "queqiao",
  "tiers": {
    "fast":        {"group": "qq-fast", "claude_alias": "haiku",   "criteria": "Little work"},
    "balanced":    {"group": "qq-balanced", "claude_alias": "sonnet", "criteria": "Some work"},
    "performance": {"group": "qq-perf", "claude_alias": "opus",    "criteria": "Much work"}
  },
  "default_tier": "balanced",
  "classifier": "fake/cls",
  "classify_timeout_ms": 1500,
  "thresholds": {"tier_min": 0.4, "dissatisfied_min": 0.7},
  "escalate_turns": 2,
  "cache_ttl_seconds": 300
}`
	if err := os.MkdirAll(filepath.Dir(routerJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routerJSON, []byte(criteria), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := router.Load(routerJSON, "")
	if err != nil {
		t.Fatal(err)
	}

	// the fake Jev: fast for every tier question, "yes" only on the 不对 turn
	deps := &router.Deps{Config: cfg, Classify: router.NewClassifier(cfg, func(_ context.Context, _, body string) (string, error) {
		var q struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal([]byte(body), &q)
		prompt := ""
		if len(q.Messages) > 0 {
			prompt = q.Messages[0].Content
		}
		if strings.HasPrefix(prompt, "Does the following message") {
			if strings.Contains(prompt, "不对") {
				return "yes", nil
			}
			return "no", nil
		}
		return "1", nil // fast, confidently
	})}

	// wire the hook exactly as router_wiring.go does
	managed := map[string]bool{cfg.RouterGroup: true}
	for _, tc := range cfg.Tiers {
		managed[tc.Group] = true
	}
	gateway.SetRouterHook(router.NewHook(deps).GatewayHookFunc, func(g provider.Group) bool {
		return managed[g.ID]
	})
	t.Cleanup(func() { gateway.SetRouterHook(nil, nil) })

	// the /turn endpoints on their own mux, the gateway beside them
	turnMux := http.NewServeMux()
	router.Register(turnMux, deps)
	turnSrv := httptest.NewServer(turnMux)
	t.Cleanup(turnSrv.Close)
	gw := httptest.NewServer(gateway.New().Handler())
	t.Cleanup(gw.Close)

	// the mod's pattern, three turns of §8's script
	const session = "cc-e2e"
	turn := func(prompt string) (tier, group string) {
		t.Helper()
		res, err := http.Post(turnSrv.URL+"/v1/queqiao/turn", "application/json",
			strings.NewReader(`{"harness":"claude-code","session":"`+session+`","prompt":`+jsonStr(prompt)+`,"agent":"main","store_hint":false}`))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct {
			Tier  string `json:"tier"`
			Group string `json:"group"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Tier == "" || body.Group == "" {
			t.Fatalf("/turn gave %+v", body)
		}
		// the model request, as Claude Code sends it, with the session id
		req, _ := http.NewRequest("POST", gw.URL+"/v1/messages",
			strings.NewReader(`{"model":"`+body.Group+`","max_tokens":16,"messages":[{"role":"user","content":`+jsonStr(prompt)+`}]}`))
		req.Header.Set("Authorization", "Bearer magpie")
		req.Header.Set("x-claude-code-session-id", session)
		rec, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		rb, _ := io.ReadAll(rec.Body)
		rec.Body.Close()
		if rec.StatusCode != 200 {
			t.Fatalf("%s on %s: %d %s", prompt, body.Group, rec.StatusCode, string(rb))
		}
		return body.Tier, body.Group
	}

	t1, g1 := turn("这个仓库用什么 license？")
	t2, g2 := turn("不对，我说的是那个 fork 出来的仓库的 license")
	t3, g3 := turn("继续，把两种都列出来")

	t.Logf("turns: %s/%s → %s/%s → %s/%s", t1, g1, t2, g2, t3, g3)
	if t1 != "fast" || g1 != "group/qq-fast" {
		t.Errorf("turn 1: %s %s, want fast group/qq-fast", t1, g1)
	}
	if t2 != "balanced" || g2 != "group/qq-balanced" {
		t.Errorf("turn 2: %s %s, want balanced (R3 escalates on 不对)", t2, g2)
	}
	if t3 != "balanced" || g3 != "group/qq-balanced" {
		t.Errorf("turn 3: %s %s, want balanced (R4 holds)", t3, g3)
	}
	// each tier's member served exactly its turns
	if n := ups["a"].n(); n != 1 {
		t.Errorf("fast upstream calls: %d, want 1", n)
	}
	if n := ups["b"].n(); n != 2 {
		t.Errorf("balanced upstream calls: %d, want 2", n)
	}
	if n := ups["c"].n(); n != 0 {
		t.Errorf("performance upstream calls: %d, want 0", n)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
