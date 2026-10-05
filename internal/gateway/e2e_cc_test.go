//go:build e2e

package gateway_test

/**
 * §8's end-to-end case, Claude Code half: the mod's calling pattern (POST
 * /v1/queqiao/turn, then an Anthropic Messages request carrying the
 * returned group and the session header) over a gateway with fake tier
 * upstreams and a scripted classifier. Three turns of §8's script — a
 * simple question, a "不对" turn, a carry-on turn — land on fast,
 * balanced (R3 escalates on dissatisfaction), balanced (R4 holds).
 *
 * The classifier is scripted at the ask callback (a fake Jev), not over
 * HTTP: the System One wire path is covered by the classify tests. Run
 * with `go test -tags nogui,e2e ./internal/gateway -run E2E`.
 */

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestE2EClaudeCodePattern(t *testing.T) {
	// every tier question answers fast; the 不对 turn says dissatisfied
	e := setupE2E(t, e2eScript("1"))

	const session = "cc-e2e"
	turn := func(prompt string) (tier, group string) {
		t.Helper()
		res, err := http.Post(e.TurnSrv.URL+"/v1/queqiao/turn", "application/json",
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
		req, _ := http.NewRequest("POST", e.Gw.URL+"/v1/messages",
			strings.NewReader(`{"model":"`+body.Group+`","max_tokens":16,"messages":[{"role":"user","content":`+jsonStr(prompt)+`}]}`))
		req.Header.Set("Authorization", "Bearer magpie")
		req.Header.Set("x-claude-code-session-id", session)
		rec, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		rec.Body.Close()
		if rec.StatusCode != 200 {
			t.Fatalf("%s on %s: %d", prompt, body.Group, rec.StatusCode)
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

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
