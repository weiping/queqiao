//go:build e2e

package gateway_test

/**
 * §8's end-to-end case, Codex half: the plugin's calling pattern — the
 * UserPromptSubmit handler (the real one from internal/harness/codex,
 * driven in-process) posts /turn with a turn id, then the request itself
 * goes out as group/queqiao with the x-codex-turn-metadata header, and
 * the gateway routes it by the stored hint. Same three-turn script as
 * the Claude Code half: fast, balanced (R3), balanced (R4).
 *
 * Run with `go test -tags nogui,e2e ./internal/gateway -run E2E`.
 */

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/harness"
	"github.com/yetone/magpie/internal/harness/codex"
)

func TestE2ECodexPattern(t *testing.T) {
	e := setupE2E(t, e2eScript("1"))
	client := &harness.Client{Base: e.TurnSrv.URL, HTTP: e.TurnSrv.Client()}

	const session = "codex-e2e"
	turn := func(turnID, prompt string) {
		t.Helper()
		// the hook Codex runs, with the JSON it feeds it
		stdin := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"` + session +
			`","turn_id":"` + turnID + `","cwd":"/w","prompt":` + jsonStr(prompt) + `,"model":"group/queqiao","permission_mode":"bypassPermissions"}`)
		if out, err := codex.UserPrompt(context.Background(), stdin, client); out != nil || err != nil {
			t.Fatalf("user-prompt hook: %v %v", out, err)
		}
		// the request Codex then sends: Responses protocol, routed group,
		// turn metadata in the header Codex 0.160 sends
		header := `{"session_id":"` + session + `","turn_id":"` + turnID + `","model":"group/queqiao"}`
		body := `{"model":"group/queqiao","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":` + jsonStr(prompt) + `}]}]}`
		req, _ := http.NewRequest("POST", e.Gw.URL+"/v1/responses", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer magpie")
		req.Header.Set("x-codex-turn-metadata", header)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s (turn %s): %d", prompt, turnID, res.StatusCode)
		}
	}

	turn("t-codex-1", "这个仓库用什么 license？")
	turn("t-codex-2", "不对，我说的是那个 fork 出来的仓库的 license")
	turn("t-codex-3", "继续，把两种都列出来")

	// the same three tiers §8 asks for, now over the Responses path
	// (reason strings are the policy's own; unit tests cover them)
	got := e.decisions(t, 3)
	for i, want := range []string{"fast", "balanced", "balanced"} {
		if string(got[i].Tier) != want {
			t.Errorf("decision %d: tier %s, want %s (%+v)", i, got[i].Tier, want, got[i])
		}
	}
	// hint-routed: each request landed on its tier's member
	if n := e.Ups["a"].n(); n != 1 {
		t.Errorf("fast upstream calls: %d, want 1", n)
	}
	if n := e.Ups["b"].n(); n != 2 {
		t.Errorf("balanced upstream calls: %d, want 2", n)
	}
	if n := e.Ups["c"].n(); n != 0 {
		t.Errorf("performance upstream calls: %d, want 0", n)
	}
}
