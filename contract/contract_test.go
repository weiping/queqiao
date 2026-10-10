//go:build contract

// Package contract checks what mbridge relies on in official magpie (SP8
// §2.2, §8): the CLI and HTTP surfaces internal/magpie uses. It runs daily
// against the newest magpie release; red means magpie changed something
// mbridge reads.
//
//	go test -tags contract ./contract/ -magpie=/path/to/magpie
package contract

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weiping/magpie-bridge/internal/magpie"
	"github.com/weiping/magpie-bridge/internal/testmagpie"
	"github.com/weiping/magpie-bridge/internal/wire"
)

func start(t *testing.T) (*testmagpie.Magpie, *magpie.Client) {
	t.Helper()
	up := testmagpie.NewUpstream(t)
	m := testmagpie.Start(t, testmagpie.Bin(t), up, "m1", "m2")
	c := magpie.New(m.URL)
	c.Bin, c.Run = m.Bin, m.Runner()
	return m, c
}

func post(t *testing.T, m *testmagpie.Magpie, path, body string, h map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("POST", m.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// read the whole answer: magpie records usage once its response is done
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d", path, res.StatusCode)
	}
}

// usageWhen reads today's usage until want holds, for up to 5 s: magpie
// writes a usage row after its response, so a read right after can come
// first. On the deadline it fails with the rows, the raw CSV and magpie's
// own log, so the cause shows (ubuntu CI, 2026-10-10: every row missing,
// twice, then green on a re-run; not reproduced locally).
func usageWhen(t *testing.T, m *testmagpie.Magpie, c *magpie.Client, want func([]magpie.UsageRow) bool) []magpie.UsageRow {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := c.Usage(ctx, "today") // fails when a required column is gone
		if err != nil {
			t.Fatal(err)
		}
		if want(rows) {
			return rows
		}
		if time.Now().After(deadline) {
			raw, _ := m.Run("usage", "--csv", "today")
			t.Fatalf("usage never showed the rows: %+v\nmagpie usage --csv today:\n%s\nmagpie serve log:\n%s", rows, raw, m.Log())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// `magpie group add <id> …` makes the group under exactly that id.
func TestContractGroupAddThenList(t *testing.T) {
	_, c := start(t)
	ctx := context.Background()
	if err := c.GroupAdd(ctx, "mb-fast", []string{"fake/m1"}); err != nil {
		t.Fatal(err)
	}
	ids, err := c.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids, "mb-fast") {
		t.Fatalf("groups %v lack mb-fast", ids)
	}
}

// /v1/models lists a group as group/<id>, which router check reads.
func TestContractModelsListsGroups(t *testing.T) {
	_, c := start(t)
	ctx := context.Background()
	if err := c.GroupAdd(ctx, "mb-perf", []string{"fake/m2"}); err != nil {
		t.Fatal(err)
	}
	ids, err := c.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids, magpie.GroupPrefix+"mb-perf") {
		t.Fatalf("/v1/models %v lacks group/mb-perf", ids)
	}
}

// /v1/systemone exists on loopback without a key, and a decider magpie
// lacks is a clear 4xx with magpie's own words, not a silent hang.
func TestContractSystemOneUnknownDeciderIsClearError(t *testing.T) {
	_, c := start(t)
	_, err := c.SystemOne(context.Background(), []byte(`{"model":"typesafe/jev-latest","questions":[]}`))
	var me *magpie.Error
	if !errors.As(err, &me) || me.Status < 400 || me.Status >= 500 || me.Msg == "" {
		t.Fatalf("err %v (%#v)", err, me)
	}
}

// `magpie usage --csv` carries every column the report reads, and the
// session a request named.
func TestContractSessionHeaderReachesUsage(t *testing.T) {
	m, c := start(t)
	ctx := context.Background()
	if err := c.GroupAdd(ctx, "mb-fast", []string{"fake/m1"}); err != nil {
		t.Fatal(err)
	}
	post(t, m, "/v1/chat/completions", `{"model":"group/mb-fast","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{wire.SessionHeader: "s1"})
	usageWhen(t, m, c, func(rows []magpie.UsageRow) bool {
		for _, r := range rows {
			if r.Session == "s1" && r.RequestedModel == "group/mb-fast" && r.Status == 200 {
				return true
			}
		}
		return false
	})
}

// The session a Claude Code request names reaches magpie's usage as wire
// reads it, on every turn of the conversation.
func TestContractSessionHeadersMatchWire(t *testing.T) {
	m, c := start(t)
	h := http.Header{}
	h.Set("x-claude-code-session-id", "cc-sess-1")
	turn1 := `{"model":"fake/m1","max_tokens":16,"messages":[{"role":"user","content":"first"}]}`
	turn2 := `{"model":"fake/m1","max_tokens":16,"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"ok"},{"role":"user","content":"second"}]}`
	for i, body := range []string{turn1, turn2} {
		post(t, m, "/v1/messages", body, map[string]string{"x-claude-code-session-id": "cc-sess-1", "anthropic-version": "2023-06-01"})
		req, err := wire.Parse(wire.Anthropic, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := wire.TurnOf(req); n != i+1 {
			t.Fatalf("turn %d read as %d", i+1, n)
		}
	}
	rows := usageWhen(t, m, c, func(rows []magpie.UsageRow) bool {
		n := 0
		for _, r := range rows {
			if r.Session == wire.SessionOf(h) {
				n++
			}
		}
		return n >= 2
	})
	n := 0
	for _, r := range rows {
		if r.Session == wire.SessionOf(h) {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d rows under %q, want 2: %+v", n, wire.SessionOf(h), rows)
	}
}
