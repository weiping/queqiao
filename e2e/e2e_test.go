//go:build e2e

// Package e2e drives queqiao's three harness paths through an in-process
// queqiaod in front of a real official magpie (SP8 §8), with a fake
// vendor serving every model:
//
//	go test -tags e2e ./e2e/ -magpie=/path/to/magpie
//
// The three tiers are the models m-fast, m-bal and m-perf; the classifier
// is the model clf, which answers fast unless the message says 不对.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weiping/queqiao/internal/harness"
	"github.com/weiping/queqiao/internal/harness/codex"
	"github.com/weiping/queqiao/internal/magpie"
	"github.com/weiping/queqiao/internal/proxy"
	"github.com/weiping/queqiao/internal/router"
	"github.com/weiping/queqiao/internal/testmagpie"
)

type env struct {
	Up      *testmagpie.Upstream
	Magpie  *testmagpie.Magpie
	Queqiao *httptest.Server
}

func classify(c testmagpie.Call) string {
	if c.Model != "clf" {
		return "ok"
	}
	msg := c.Content[strings.LastIndex(c.Content, "Message:")+1:]
	if strings.Contains(msg, "不对") {
		return `{"tier":"balanced","confidence":0.9,"dissatisfied":0.95}`
	}
	return `{"tier":"fast","confidence":0.9,"dissatisfied":0}`
}

func setup(t *testing.T) *env {
	t.Helper()
	t.Setenv("QUEQIAO_CONFIG_DIR", t.TempDir())
	up := testmagpie.NewUpstream(t)
	up.Answer = classify
	m := testmagpie.Start(t, testmagpie.Bin(t), up, "m-fast", "m-bal", "m-perf", "clf")
	c := magpie.New(m.URL)
	c.Bin, c.Run = m.Bin, m.Runner()
	ctx := context.Background()
	// the tier groups first: the router group names them
	for _, g := range []struct {
		id      string
		members []string
	}{
		{"qq-fast", []string{"fake/m-fast"}}, {"qq-balanced", []string{"fake/m-bal"}}, {"qq-perf", []string{"fake/m-perf"}},
		{"queqiao", []string{"group/qq-balanced", "group/qq-perf", "group/qq-fast"}},
	} {
		if err := c.GroupAdd(ctx, g.id, g.members); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "router.json")
	cfg := `{"version":1,"router_group":"queqiao","default_tier":"balanced","classifier":"fake/clf",
	  "classify_timeout_ms":5000,"magpie_url":` + jsonStr(m.URL) + `,
	  "tiers":{"fast":{"group":"qq-fast","claude_alias":"haiku","criteria":"little work"},
	           "balanced":{"group":"qq-balanced","claude_alias":"sonnet","criteria":"some work"},
	           "performance":{"group":"qq-perf","claude_alias":"opus","criteria":"much work"}}}`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := router.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	deps := &router.Deps{Config: rc, Classify: router.NewClassifier(rc, router.AskVia(c))}
	target, _ := url.Parse(m.URL)
	q := httptest.NewServer(proxy.Handler(target, deps))
	t.Cleanup(q.Close)
	return &env{Up: up, Magpie: m, Queqiao: q}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func postJSON(t *testing.T, url, body string, h map[string]string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d %s", url, res.StatusCode, b.String())
	}
	out := map[string]any{}
	json.Unmarshal(b.Bytes(), &out)
	return out
}

// tierCalls counts the vendor's calls per tier model, the classifier's
// left out.
func tierCalls(u *testmagpie.Upstream) map[string]int {
	m := u.Models()
	delete(m, "clf")
	return m
}

var script = []string{"这个仓库用什么 license？", "不对，我说的是那个 fork 出来的仓库的 license", "继续，把两种都列出来"}

// Claude Code: the mod asks /turn, then Claude Code's own request goes
// straight to magpie on the tier group /turn named.
func TestE2EClaudeCode(t *testing.T) {
	e := setup(t)
	var tiers []string
	for _, prompt := range script {
		res := postJSON(t, e.Queqiao.URL+"/v1/queqiao/turn",
			`{"session":"cc-e2e","prompt":`+jsonStr(prompt)+`,"harness":"claude-code","agent":"main","tool_calls":0,"tool_failures":0}`, nil)
		tiers = append(tiers, res["tier"].(string))
		body := `{"model":` + jsonStr(res["group"].(string)) + `,"max_tokens":32,"stream":true,"messages":[{"role":"user","content":` + jsonStr(prompt) + `}]}`
		postJSON(t, e.Magpie.URL+"/v1/messages", body, map[string]string{"x-claude-code-session-id": "cc-e2e", "anthropic-version": "2023-06-01"})
	}
	if strings.Join(tiers, ",") != "fast,balanced,balanced" {
		t.Fatalf("tiers %v", tiers)
	}
	if got := tierCalls(e.Up); got["m-fast"] != 1 || got["m-bal"] != 2 || got["m-perf"] != 0 {
		t.Fatalf("vendor calls %v", got)
	}
}

// Codex: the UserPromptSubmit hook stores a hint through /turn; Codex's
// request goes to queqiaod as group/queqiao and is rewritten to the
// hinted tier; a second request in the same turn keeps it.
func TestE2ECodex(t *testing.T) {
	e := setup(t)
	client := &harness.Client{Base: e.Queqiao.URL, HTTP: http.DefaultClient}
	const session = "codex-e2e"
	for i, prompt := range script {
		turnID := "t-" + string(rune('1'+i))
		stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"` + session + `","turn_id":"` + turnID +
			`","cwd":"/w","prompt":` + jsonStr(prompt) + `,"model":"group/queqiao","permission_mode":"bypassPermissions"}`)
		if out, err := codex.UserPrompt(context.Background(), stdin, client); out != nil || err != nil {
			t.Fatalf("hook: %s %v", out, err)
		}
		meta := `{"session_id":"` + session + `","turn_id":"` + turnID + `"}`
		body := `{"model":"group/queqiao","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":` + jsonStr(prompt) + `}]}]}`
		h := map[string]string{"x-codex-turn-metadata": meta, "session_id": session, "originator": "codex_cli_rs", "Authorization": "Bearer magpie"}
		postRaw(t, e.Queqiao.URL+"/v1/responses", body, h)
		if i == 2 { // a tool round in the same turn: same tier
			second := `{"model":"group/queqiao","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":` + jsonStr(prompt) + `}]},{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"done"}]}`
			postRaw(t, e.Queqiao.URL+"/v1/responses", second, h)
		}
	}
	if got := tierCalls(e.Up); got["m-fast"] != 1 || got["m-bal"] != 3 || got["m-perf"] != 0 {
		t.Fatalf("vendor calls %v (want fast 1, balanced 3)", got)
	}
	// every request reached magpie rewritten: magpie's own fallback for
	// group/queqiao (balanced first) would land on m-bal too, so the
	// model magpie was asked for is what tells them apart
	c := magpie.New(e.Magpie.URL)
	c.Bin, c.Run = e.Magpie.Bin, e.Magpie.Runner()
	rows, err := c.Usage(context.Background(), "today")
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	slices.Reverse(rows) // the CSV lists the newest first
	for _, r := range rows {
		if r.Session == session && r.RequestedModel != "group/clf" && !strings.HasSuffix(r.RequestedModel, "/clf") {
			asked = append(asked, r.RequestedModel)
		}
	}
	if strings.Join(asked, ",") != "group/qq-fast,group/qq-balanced,group/qq-balanced,group/qq-balanced" {
		t.Fatalf("magpie was asked for %v", asked)
	}
}

// Pi: /turn carries the extension's own tool counts; a turn after one
// whose tools mostly failed goes up a tier (R3-tools).
func TestE2EPiToolStats(t *testing.T) {
	e := setup(t)
	first := postJSON(t, e.Queqiao.URL+"/v1/queqiao/turn", `{"session":"pi-e2e","prompt":"列出文件","harness":"pi","agent":"main"}`, nil)
	if first["tier"] != "fast" {
		t.Fatalf("first turn %v", first)
	}
	second := postJSON(t, e.Queqiao.URL+"/v1/queqiao/turn",
		`{"session":"pi-e2e","prompt":"继续","harness":"pi","agent":"main","tool_calls":4,"tool_failures":3}`, nil)
	if second["tier"] != "balanced" || second["reason"] != "R3-tools" {
		t.Fatalf("second turn %v", second)
	}
	// and the request Pi then sends lands on that tier through magpie
	postJSON(t, e.Magpie.URL+"/v1/chat/completions", `{"model":`+jsonStr(second["group"].(string))+`,"messages":[{"role":"user","content":"继续"}]}`,
		map[string]string{"X-Magpie-Session": "pi-e2e"})
	if got := tierCalls(e.Up); got["m-bal"] != 1 {
		t.Fatalf("vendor calls %v", got)
	}
}

func postRaw(t *testing.T, url, body string, h map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.ReadFrom(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d %s", url, res.StatusCode, b.String())
	}
}
