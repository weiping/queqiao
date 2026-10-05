package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeClassifier returns a fixed verdict and records the questions asked.
type fakeClassifier struct {
	verdict   *Verdict
	err       error
	questions []Question
}

func (f *fakeClassifier) Classify(ctx context.Context, q Question) (*Verdict, error) {
	f.questions = append(f.questions, q)
	return f.verdict, f.err
}

// testDeps builds Deps around a fake classifier and an in-memory event log.
func testDeps(t *testing.T, verdict *Verdict) (*Deps, *fakeClassifier, *[]Event) {
	t.Helper()
	cfg, err := Load(writeGlobal(t, validJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	fc := &fakeClassifier{verdict: verdict}
	d := &Deps{Config: cfg, Sessions: NewSessions(), Hints: NewHints(), Classify: fc,
		Log: func(ev Event) error { events = append(events, ev); return nil }}
	return d, fc, &events
}

func testServer(d *Deps) *httptest.Server {
	mux := http.NewServeMux()
	Register(mux, d)
	return httptest.NewServer(mux)
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestTurnReturnsTierAndReason(t *testing.T) {
	d, fc, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0.1})
	srv := testServer(d)
	defer srv.Close()

	res := postJSON(t, srv.URL+"/v1/queqiao/turn", map[string]any{
		"session": "s1", "prompt": "hello there", "harness": "claude-code",
	})
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out struct {
		Tier        Tier    `json:"tier"`
		Group       string  `json:"group"`
		ClaudeAlias string  `json:"claude_alias"`
		Reason      string  `json:"reason"`
		Source      string  `json:"source"`
		Confidence  float64 `json:"confidence"`
		Arm         string  `json:"arm"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Tier != TierBalanced || out.Group != "group/qq-balanced" || out.ClaudeAlias != "sonnet" {
		t.Fatalf("out: %+v", out)
	}
	if out.Reason != "R6-adopt" || out.Source != "llm" || out.Confidence != 0.9 || out.Arm != "router" {
		t.Fatalf("out: %+v", out)
	}
	if len(fc.questions) != 1 || fc.questions[0].Message != "hello there" {
		t.Fatalf("classifier asked: %+v", fc.questions)
	}
	// the session state was committed (first turn → R6-adopt)
	if st := d.Sessions.Get("s1"); st == nil || st.Tier != TierBalanced {
		t.Fatalf("state: %+v", st)
	}
}

func TestTurnRequiresSessionAndPrompt(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	srv := testServer(d)
	defer srv.Close()
	for _, body := range []map[string]any{
		{"prompt": "hi"},
		{"session": "s"},
	} {
		res := postJSON(t, srv.URL+"/v1/queqiao/turn", body)
		if res.StatusCode != 400 {
			t.Fatalf("body %v: status %d", body, res.StatusCode)
		}
	}
}

func TestTurnControlArmReturnsControlTier(t *testing.T) {
	d, _, events := testDeps(t, &Verdict{Tier: TierFast, TierConfidence: 0.9, Dissatisfied: 0})
	d.Config.Experiment = ExperimentConfig{Enabled: true, RouterPercent: 0, ControlTier: TierPerformance, Salt: "s"}
	// RouterPercent 0 → always control
	srv := testServer(d)
	defer srv.Close()
	res := postJSON(t, srv.URL+"/v1/queqiao/turn", map[string]any{"session": "any", "prompt": "hi"})
	var out struct {
		Tier Tier   `json:"tier"`
		Arm  string `json:"arm"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Arm != "control" || out.Tier != TierPerformance {
		t.Fatalf("control arm: %+v", out)
	}
	// the router's own choice was logged as a shadow decision
	found := false
	for _, ev := range *events {
		if ev.Kind == "shadow" && ev.ShadowTier == TierFast {
			found = true
		}
	}
	if !found {
		t.Fatalf("no shadow event in %v", *events)
	}
}

func TestTurnStoresHint(t *testing.T) {
	d, _, _ := testDeps(t, &Verdict{Tier: TierFast, TierConfidence: 0.9, Dissatisfied: 0})
	srv := testServer(d)
	defer srv.Close()
	res := postJSON(t, srv.URL+"/v1/queqiao/turn", map[string]any{
		"session": "codex-s", "prompt": "do a thing", "harness": "codex",
		"turn_id": "t-42", "store_hint": true,
	})
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	hint, ok := d.Hints.Take(HintKey{Session: "codex-s", TurnID: "t-42"})
	if !ok || hint.Tier != TierFast {
		t.Fatalf("hint: %+v %v", hint, ok)
	}
}

func TestFeedback204AndEvent(t *testing.T) {
	d, _, events := testDeps(t, nil)
	srv := testServer(d)
	defer srv.Close()
	res := postJSON(t, srv.URL+"/v1/queqiao/feedback", map[string]any{
		"session": "s1", "kind": "pr_created", "value": "https://github.com/o/r/pull/1",
	})
	if res.StatusCode != 204 {
		t.Fatalf("status %d", res.StatusCode)
	}
	found := false
	for _, ev := range *events {
		if ev.Kind == "feedback" && strings.Contains(ev.Extra, "pr_created") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no feedback event: %v", *events)
	}
}

func TestSessionEndpoint(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	d.Sessions.Commit("sess-9", TurnState{Tier: TierPerformance})
	srv := testServer(d)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/queqiao/session?id=sess-9")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tier  Tier   `json:"tier"`
		Group string `json:"group"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || out.Tier != TierPerformance || out.Group != "group/qq-perf" {
		t.Fatalf("session: %d %+v", res.StatusCode, out)
	}

	res, _ = http.Get(srv.URL + "/v1/queqiao/session?id=missing")
	if res.StatusCode != 404 {
		t.Fatalf("missing session: %d", res.StatusCode)
	}
}

func TestLineageMarksDerived(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	srv := testServer(d)
	defer srv.Close()
	res := postJSON(t, srv.URL+"/v1/queqiao/lineage", map[string]any{
		"session": "pi-fork", "parent_session": "pi-main", "source": "pi-fork",
	})
	if res.StatusCode != 204 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if p, ok := d.Sessions.ParentOf("pi-fork", ""); !ok || p != "pi-main" {
		t.Fatalf("ParentOf: %q %v", p, ok)
	}
}

func TestRouterStatus(t *testing.T) {
	d, _, _ := testDeps(t, &Verdict{Tier: TierBalanced, TierConfidence: 0.9, Dissatisfied: 0})
	srv := testServer(d)
	defer srv.Close()
	// two turns → two decisions in the ring
	for i := 0; i < 2; i++ {
		postJSON(t, srv.URL+"/v1/queqiao/turn", map[string]any{"session": "s1", "prompt": "hi"})
	}
	res, err := http.Get(srv.URL + "/v1/queqiao/router")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Valid     bool                         `json:"valid"`
		Tiers     map[string]map[string]string `json:"tiers"`
		Decisions []map[string]any             `json:"decisions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Valid || out.Tiers["fast"]["group"] != "qq-fast" {
		t.Fatalf("status: %+v", out)
	}
	if len(out.Decisions) != 2 {
		t.Fatalf("decisions: %d", len(out.Decisions))
	}
}

func TestDecideParentInheritance(t *testing.T) {
	d, _, _ := testDeps(t, &Verdict{Tier: TierPerformance, TierConfidence: 0.9, Dissatisfied: 0})
	// parent session decided performance with escalation left
	d.Sessions.Commit("parent-s", TurnState{Tier: TierPerformance, EscalatedLeft: 1})
	res := d.Decide(context.Background(), DecideInput{
		Session: "fork-s", Key: "fork-s", Agent: "main",
		Prompt: "carry on", ParentSession: "parent-s",
	})
	// R4 holds performance (classified performance too)
	if res.Tier != TierPerformance || res.Reason != "R4-escalation-hold" {
		t.Fatalf("inherit: %+v", res)
	}
	st := d.Sessions.Get("fork-s")
	if st == nil || st.EscalatedLeft != 0 {
		t.Fatalf("fork state: %+v", st)
	}
}

func TestDecideSubagentStateless(t *testing.T) {
	d, fc, _ := testDeps(t, &Verdict{Tier: TierFast, TierConfidence: 0.9, Dissatisfied: 0})
	// main session commits balanced
	d.Sessions.Commit("s|main", TurnState{Tier: TierBalanced})
	res := d.Decide(context.Background(), DecideInput{
		Session: "s", Key: "s|main", Agent: "general-purpose", Prompt: "look around",
	})
	if res.Tier != TierFast || res.Reason != "R6-adopt" {
		t.Fatalf("subagent: %+v", res)
	}
	if len(fc.questions) == 0 {
		t.Fatal("subagent not classified")
	}
	// subagent decision did not touch the main session's state
	if st := d.Sessions.Get("s|main"); st == nil || st.Tier != TierBalanced {
		t.Fatalf("main state clobbered: %+v", st)
	}
}

func TestDecideFixedAgentSkipsClassifier(t *testing.T) {
	d, fc, _ := testDeps(t, nil)
	res := d.Decide(context.Background(), DecideInput{
		Session: "s", Key: "s", Agent: "Explore", Prompt: "scan",
	})
	if res.Tier != TierFast || res.Reason != "R1-fixed" {
		t.Fatalf("fixed: %+v", res)
	}
	if len(fc.questions) != 0 {
		t.Fatalf("classifier asked anyway: %d", len(fc.questions))
	}
	if st := d.Sessions.Get("s"); st != nil {
		t.Fatalf("R1 wrote state: %+v", st)
	}
}

func TestPromptHashNormalization(t *testing.T) {
	a := PromptHash("<system-reminder>Codebase context</system-reminder>\nhello\r\nworld  ")
	b := PromptHash("hello\nworld")
	if a != b {
		t.Fatalf("hashes differ: %s vs %s", a, b)
	}
	if PromptHash("") == PromptHash("x") {
		t.Fatal("empty and x collide")
	}
}

func TestDecideGatewayToolStats(t *testing.T) {
	d, _, _ := testDeps(t, nil)
	// gateway mode with harness-reported stats: half of 4 calls failed → R3
	d.Sessions.Commit("gw-key", TurnState{Tier: TierFast})
	res := d.Decide(context.Background(), DecideInput{
		Session: "gw-s", Key: "gw-key", Agent: "gateway",
		HasToolStats: true, ToolCalls: 4, ToolFailures: 2,
	})
	if res.Tier != TierBalanced || res.Reason != "R3-escalate" {
		t.Fatalf("gateway stats: %+v", res)
	}
}
