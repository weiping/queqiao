//go:build e2e

package gateway_test

/**
 * The shared end-to-end environment (§8): three fake tier upstreams, the
 * four groups, router.json, a scripted classifier and the wired router —
 * used by both the Claude Code pattern (e2e_cc_test.go) and the Codex
 * pattern (e2e_codex_test.go).
 */

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/forkhook"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/router"
)

// e2eUp is a fake OpenAI-compatible upstream that records its calls.
type e2eUp struct {
	ch    chan struct{}
	calls int
}

func newE2EUp() *e2eUp { return &e2eUp{ch: make(chan struct{}, 128)} }

func (u *e2eUp) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	u.calls++
	u.ch <- struct{}{}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []string{
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
		`data: [DONE]`,
	} {
		w.Write([]byte(chunk + "\n\n"))
	}
}

func (u *e2eUp) n() int { return u.calls }

// askClassify answers the plain classifier's two questions: a numbered
// tier for "which tier fits" prompts and yes/no for the dissatisfied one.
// The dissatisfied answer keys on the 不对 marker, so exactly one turn of
// the §8 script escalates and the next holds.
type askClassify func(prompt string) string

func e2eScript(tierReply string) askClassify {
	return func(prompt string) string {
		if strings.HasPrefix(prompt, "Does the following message") {
			if strings.Contains(prompt, "不对") {
				return "yes"
			}
			return "no"
		}
		return tierReply
	}
}

type e2eEnv struct {
	Ups     map[string]*e2eUp // "a" fast, "b" balanced, "c" performance
	TurnSrv *httptest.Server  // the /v1/queqiao/* endpoints
	Gw      *httptest.Server  // the gateway itself
	Cfg     router.Config
}

func setupE2E(t *testing.T, ask askClassify) *e2eEnv {
	t.Helper()
	// a clean config home, as the gateway tests' fresh() arranges
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	ups := map[string]*e2eUp{}
	for _, x := range []struct{ id, model string }{
		{"a", "fastm"}, {"b", "balm"}, {"c", "perfm"},
	} {
		u := newE2EUp()
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

	routerJSON := filepath.Join(home, ".config", "queqiao", "router.json")
	const cfg = `{
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
	if err := os.WriteFile(routerJSON, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rcfg, err := router.Load(routerJSON, "")
	if err != nil {
		t.Fatal(err)
	}

	deps := &router.Deps{Config: rcfg, Classify: router.NewClassifier(rcfg, func(_ context.Context, _, body string) (string, error) {
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
		return ask(prompt), nil
	})}

	// wire the hook exactly as router_wiring.go does
	managed := map[string]bool{rcfg.RouterGroup: true}
	for _, tc := range rcfg.Tiers {
		managed[tc.Group] = true
	}
	gateway.SetRouterHook(forkhook.GatewayHook(router.NewRouter(deps), rcfg.RouterGroup), func(g provider.Group) bool {
		return managed[g.ID]
	})
	t.Cleanup(func() { gateway.SetRouterHook(nil, nil) })

	turnMux := http.NewServeMux()
	router.Register(turnMux, deps)
	turnSrv := httptest.NewServer(turnMux)
	t.Cleanup(turnSrv.Close)
	gw := httptest.NewServer(gateway.New().Handler())
	t.Cleanup(gw.Close)

	return &e2eEnv{Ups: ups, TurnSrv: turnSrv, Gw: gw, Cfg: rcfg}
}

// decisions reads the last n decisions from the status endpoint.
func (e *e2eEnv) decisions(t *testing.T, n int) []router.Event {
	t.Helper()
	res, err := http.Get(e.TurnSrv.URL + "/v1/queqiao/router")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Decisions []router.Event `json:"decisions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Decisions) < n {
		t.Fatalf("decisions: %d, want at least %d", len(out.Decisions), n)
	}
	return out.Decisions[len(out.Decisions)-n:]
}
