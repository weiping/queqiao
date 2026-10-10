package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/weiping/magpie-bridge/internal/harness"
)

// fakeGateway records every path+body and answers /turn with scripted
// tiers, one per call.
type fakeGateway struct {
	turns []map[string]any
	posts []struct {
		path string
		body map[string]any
	}
}

func newFakeGateway(t *testing.T, replies ...map[string]any) (*fakeGateway, *harness.Client) {
	t.Helper()
	f := &fakeGateway{turns: replies}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.posts = append(f.posts, struct {
			path string
			body map[string]any
		}{r.URL.Path, body})
		if r.URL.Path == "/v1/bridge/turn" {
			if len(f.turns) == 0 {
				w.WriteHeader(500)
				io.WriteString(w, `{"error":"no scripted reply"}`)
				return
			}
			reply := f.turns[0]
			f.turns = f.turns[1:]
			json.NewEncoder(w).Encode(reply)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)
	return f, &harness.Client{Base: srv.URL, HTTP: srv.Client()}
}

func (f *fakeGateway) at(path string) []map[string]any {
	var out []map[string]any
	for _, p := range f.posts {
		if p.path == path {
			out = append(out, p.body)
		}
	}
	return out
}

func TestUserPromptRoutedModelCallsTurn(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "fast", "group": "group/mb-fast"})
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t9","cwd":"/w","prompt":"what license?","model":"group/mbridge","permission_mode":"bypassPermissions"}`)
	out, err := UserPrompt(context.Background(), stdin, c)
	if err != nil || out != nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	turns := f.at("/v1/bridge/turn")
	if len(turns) != 1 {
		t.Fatalf("turns: %d", len(turns))
	}
	body := turns[0]
	for k, want := range map[string]any{
		"harness":    "codex",
		"session":    "s1",
		"turn_id":    "t9",
		"cwd":        "/w",
		"prompt":     "what license?",
		"agent":      "main",
		"plan_mode":  false,
		"store_hint": true,
	} {
		if body[k] != want {
			t.Fatalf("%s: got %v, want %v", k, body[k], want)
		}
	}
}

func TestUserPromptPinnedModelReportsInsteadOfRouting(t *testing.T) {
	f, c := newFakeGateway(t)
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"hi","model":"gpt-6-sol"}`)
	if out, err := UserPrompt(context.Background(), stdin, c); out != nil || err != nil {
		t.Fatal("unexpected output")
	}
	if len(f.at("/v1/bridge/turn")) != 0 {
		t.Fatal("pinned model still routed")
	}
	fb := f.at("/v1/bridge/feedback")
	if len(fb) != 1 || fb[0]["kind"] != "manual_model_switch" || fb[0]["value"] != "gpt-6-sol" {
		t.Fatalf("feedback: %v", fb)
	}
}

func TestUserPromptCarriesForkedFromThread(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "balanced", "group": "group/mb-balanced"})
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s2","prompt":"go on","model":"group/mbridge","thread":{"forked_from_thread_id":"th-parent"}}`)
	UserPrompt(context.Background(), stdin, c)
	turns := f.at("/v1/bridge/turn")
	if len(turns) != 1 || turns[0]["parent_session"] != "th-parent" {
		t.Fatalf("parent: %v", turns)
	}
}

func TestUserPromptDefensiveSkips(t *testing.T) {
	f, c := newFakeGateway(t)
	for _, stdin := range []string{
		`not json at all`,
		`{"session_id":"","prompt":"hi"}`,
		`{"session_id":"s","prompt":""}`,
	} {
		if out, err := UserPrompt(context.Background(), []byte(stdin), c); out != nil || err != nil {
			t.Fatalf("%s: %v %v", stdin, out, err)
		}
	}
	if len(f.posts) != 0 {
		t.Fatalf("posts: %v", f.posts)
	}
}

func TestUserPromptTurnFailureStaysSilent(t *testing.T) {
	_, c := newFakeGateway(t) // no scripted replies → 500
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"hi","model":"group/mbridge"}`)
	if out, err := UserPrompt(context.Background(), stdin, c); out != nil || err != nil {
		t.Fatal("failure was not silent")
	}
}

func TestPreAgentPinsModelAndKeepsParams(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "fast", "group": "group/mb-fast"})
	stdin := []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"spawn_agent","tool_input":{"message":"search the repo","agent_type":"Explore"}}`)
	out, err := PreAgent(context.Background(), stdin, c)
	if err != nil || out == nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	var decoded struct {
		HookSpecificOutput struct {
			HookEventName      string         `json:"hookEventName"`
			PermissionDecision string         `json:"permissionDecision"`
			UpdatedInput       map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("output not json: %v", err)
	}
	h := decoded.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "allow" {
		t.Fatalf("decision: %+v", h)
	}
	if h.UpdatedInput["message"] != "search the repo" || h.UpdatedInput["agent_type"] != "Explore" {
		t.Fatalf("params not kept: %v", h.UpdatedInput)
	}
	if h.UpdatedInput["model"] != "group/mb-fast" {
		t.Fatalf("model: %v", h.UpdatedInput["model"])
	}
	turns := f.at("/v1/bridge/turn")
	if len(turns) != 1 || turns[0]["agent"] != "Explore" || turns[0]["prompt"] != "search the repo" || turns[0]["store_hint"] != false {
		t.Fatalf("turn: %v", turns)
	}
}

func TestPreAgentDefaultsAgentType(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "balanced", "group": "group/mb-balanced"})
	stdin := []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"spawn_agent","tool_input":{"message":"do things"}}`)
	if _, err := PreAgent(context.Background(), stdin, c); err != nil {
		t.Fatal(err)
	}
	if turns := f.at("/v1/bridge/turn"); turns[0]["agent"] != "default" {
		t.Fatalf("agent: %v", turns[0]["agent"])
	}
}

func TestPreAgentSkipsPresetModelAndFailures(t *testing.T) {
	f, c := newFakeGateway(t) // failing gateway; the bare call still posts
	preset := []byte(`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"spawn_agent","tool_input":{"message":"x","model":"gpt-6"}}`)
	if out, err := PreAgent(context.Background(), preset, c); out != nil || err != nil {
		t.Fatal("preset model was touched")
	}
	bare := []byte(`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"spawn_agent","tool_input":{"message":"x"}}`)
	if out, err := PreAgent(context.Background(), bare, c); out != nil || err != nil {
		t.Fatal("failure was not silent")
	}
	if len(f.posts) != 1 { // only the bare one reached the gateway
		t.Fatalf("posts: %v", f.posts)
	}
}

func TestPostBashFindsPRLinkAnywhere(t *testing.T) {
	f, c := newFakeGateway(t)
	stdin := []byte(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","whatever":{"nested":{"output":"Opened https://github.com/weiping/magpie-bridge/pull/12 today"}}}`)
	if out, err := PostBash(context.Background(), stdin, c); out != nil || err != nil {
		t.Fatal("unexpected output")
	}
	fb := f.at("/v1/bridge/feedback")
	if len(fb) != 1 || fb[0]["kind"] != "pr_created" || fb[0]["value"] != "https://github.com/weiping/magpie-bridge/pull/12" {
		t.Fatalf("feedback: %v", fb)
	}

	f2, c2 := newFakeGateway(t)
	PostBash(context.Background(), []byte(`{"session_id":"s","output":"no links here"}`), c2)
	if len(f2.posts) != 0 {
		t.Fatalf("posts: %v", f2.posts)
	}
}

// ---- SP7: the Stop hook's end-of-turn review ----

// TestStopPostsReview: UserPrompt leaves this turn's prompt behind, Stop
// pairs it with the final answer and posts the review.
func TestStopPostsReview(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	f, c := newFakeGateway(t, map[string]any{"tier": "fast", "group": "group/mb-fast"})
	prompt := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t9","prompt":"read ./no-such-file.md and summarise","model":"group/mbridge"}`)
	if _, err := UserPrompt(context.Background(), prompt, c); err != nil {
		t.Fatal(err)
	}
	stop := []byte(`{"hook_event_name":"Stop","session_id":"s1","turn_id":"t9","last_assistant_message":"I could not read it."}`)
	out, err := Stop(context.Background(), stop, c)
	if err != nil || out != nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	rev := f.at("/v1/bridge/review")
	if len(rev) != 1 {
		t.Fatalf("reviews: %d", len(rev))
	}
	for k, want := range map[string]any{
		"session": "s1",
		"harness": "codex",
		"turn_id": "t9",
		"prompt":  "read ./no-such-file.md and summarise",
		"answer":  "I could not read it.",
	} {
		if rev[0][k] != want {
			t.Fatalf("%s: got %v, want %v", k, rev[0][k], want)
		}
	}
}

// TestStopWithoutPromptStateIsSilent: no state file (gateway mode, a
// pinned turn), no answer, or a different turn — nothing is posted.
func TestStopWithoutPromptStateIsSilent(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	f, c := newFakeGateway(t, map[string]any{"tier": "fast", "group": "group/mb-fast"})
	for _, stdin := range []string{
		`{"hook_event_name":"Stop","session_id":"nobody","turn_id":"t1","last_assistant_message":"ok"}`,
		`{"hook_event_name":"Stop","session_id":"s1","turn_id":"t1"}`,
		`not json at all`,
	} {
		if out, err := Stop(context.Background(), []byte(stdin), c); out != nil || err != nil {
			t.Fatalf("stdin %s: out=%v err=%v", stdin, out, err)
		}
	}
	// a state file from another turn does not pair with this answer
	if _, err := UserPrompt(context.Background(), []byte(`{"session_id":"s1","turn_id":"t1","prompt":"old","model":"group/mbridge"}`), c); err != nil {
		t.Fatal(err)
	}
	Stop(context.Background(), []byte(`{"hook_event_name":"Stop","session_id":"s1","turn_id":"t2","last_assistant_message":"new answer"}`), c)
	if got := len(f.at("/v1/bridge/review")); got != 0 {
		t.Fatalf("stale state still posted %d reviews", got)
	}
}

// TestStopGatewayDownExitsQuietly: an unreachable gateway costs less than
// the 1s review budget and never errors.
func TestStopGatewayDownExitsQuietly(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	c := &harness.Client{Base: "http://127.0.0.1:1", HTTP: http.DefaultClient}
	if _, err := UserPrompt(context.Background(), []byte(`{"session_id":"s1","turn_id":"t1","prompt":"hi","model":"group/mbridge"}`), c); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := Stop(context.Background(), []byte(`{"hook_event_name":"Stop","session_id":"s1","turn_id":"t1","last_assistant_message":"ok"}`), c)
	if out != nil || err != nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("Stop waited %v on a dead gateway", elapsed)
	}
}

// TestCodexHooksJSONHasStop: the shipped hooks file carries the Stop hook
// the CLI's `mbridge hook stop` serves.
func TestCodexHooksJSONHasStop(t *testing.T) {
	b, err := os.ReadFile("../../../clients/codex/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	stops := hooks.Hooks["Stop"]
	if len(stops) != 1 || len(stops[0].Hooks) != 1 {
		t.Fatalf("Stop hooks: %+v", stops)
	}
	got := stops[0].Hooks[0]
	if got.Type != "command" || got.Command != "mbridge hook stop --harness codex" || got.Timeout != 2 {
		t.Fatalf("Stop hook: %+v", got)
	}
}

// SP10: Codex kills a hook at its "timeout" (seconds). The hooks that call
// /turn must outlive the longest turn budget mbridge can report (8000 ms),
// or a cold first turn is cut off before Jev answers.
func TestCodexTurnHooksOutliveTheTurnBudget(t *testing.T) {
	b, err := os.ReadFile("../../../clients/codex/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"UserPromptSubmit", "PreToolUse"} {
		for _, g := range hooks.Hooks[event] {
			for _, h := range g.Hooks {
				if h.Timeout*1000 <= 8000 {
					t.Errorf("%s hook %q times out at %d s; /turn may take up to 8 s", event, h.Command, h.Timeout)
				}
			}
		}
	}
}
