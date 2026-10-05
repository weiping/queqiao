package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/harness"
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
		if r.URL.Path == "/v1/queqiao/turn" {
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
	f, c := newFakeGateway(t, map[string]any{"tier": "fast", "group": "group/qq-fast"})
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t9","cwd":"/w","prompt":"what license?","model":"group/queqiao","permission_mode":"bypassPermissions"}`)
	out, err := UserPrompt(context.Background(), stdin, c)
	if err != nil || out != nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	turns := f.at("/v1/queqiao/turn")
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
	if len(f.at("/v1/queqiao/turn")) != 0 {
		t.Fatal("pinned model still routed")
	}
	fb := f.at("/v1/queqiao/feedback")
	if len(fb) != 1 || fb[0]["kind"] != "manual_model_switch" || fb[0]["value"] != "gpt-6-sol" {
		t.Fatalf("feedback: %v", fb)
	}
}

func TestUserPromptCarriesForkedFromThread(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "balanced", "group": "group/qq-balanced"})
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s2","prompt":"go on","model":"group/queqiao","thread":{"forked_from_thread_id":"th-parent"}}`)
	UserPrompt(context.Background(), stdin, c)
	turns := f.at("/v1/queqiao/turn")
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
	stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"hi","model":"group/queqiao"}`)
	if out, err := UserPrompt(context.Background(), stdin, c); out != nil || err != nil {
		t.Fatal("failure was not silent")
	}
}

func TestPreAgentPinsModelAndKeepsParams(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "fast", "group": "group/qq-fast"})
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
	if h.UpdatedInput["model"] != "group/qq-fast" {
		t.Fatalf("model: %v", h.UpdatedInput["model"])
	}
	turns := f.at("/v1/queqiao/turn")
	if len(turns) != 1 || turns[0]["agent"] != "Explore" || turns[0]["prompt"] != "search the repo" || turns[0]["store_hint"] != false {
		t.Fatalf("turn: %v", turns)
	}
}

func TestPreAgentDefaultsAgentType(t *testing.T) {
	f, c := newFakeGateway(t, map[string]any{"tier": "balanced", "group": "group/qq-balanced"})
	stdin := []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"spawn_agent","tool_input":{"message":"do things"}}`)
	if _, err := PreAgent(context.Background(), stdin, c); err != nil {
		t.Fatal(err)
	}
	if turns := f.at("/v1/queqiao/turn"); turns[0]["agent"] != "default" {
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
	stdin := []byte(`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","whatever":{"nested":{"output":"Opened https://github.com/weiping/queqiao/pull/12 today"}}}`)
	if out, err := PostBash(context.Background(), stdin, c); out != nil || err != nil {
		t.Fatal("unexpected output")
	}
	fb := f.at("/v1/queqiao/feedback")
	if len(fb) != 1 || fb[0]["kind"] != "pr_created" || fb[0]["value"] != "https://github.com/weiping/queqiao/pull/12" {
		t.Fatalf("feedback: %v", fb)
	}

	f2, c2 := newFakeGateway(t)
	PostBash(context.Background(), []byte(`{"session_id":"s","output":"no links here"}`), c2)
	if len(f2.posts) != 0 {
		t.Fatalf("posts: %v", f2.posts)
	}
}
