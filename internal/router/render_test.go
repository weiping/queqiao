package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weiping/queqiao/internal/magpie"
)

// A fake gh: a script on PATH that prints the state its filename encodes.
func fakeGh(t *testing.T, states map[string]string) (dir string, restore func()) {
	t.Helper()
	dir = t.TempDir()
	script := filepath.Join(dir, "gh")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(`case "$3" in` + "\n")
	for url, state := range states {
		b.WriteString("  " + url + ") echo '{\"state\":\"" + state + "\"}' ;;\n")
	}
	b.WriteString("  *) exit 1 ;;\nesac\n")
	if err := os.WriteFile(script, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir, func() {}
}

func TestPRStatesViaGhAndFallbacks(t *testing.T) {
	now := ts(testNow)
	events := []Event{
		{Kind: "feedback", Session: "s1", Time: now, Extra: "pr_created https://github.com/a/b/pull/1"},
		{Kind: "feedback", Session: "s2", Time: now, Extra: "pr_created https://github.com/a/b/pull/2"},
		{Kind: "feedback", Session: "s3", Time: now, Extra: "pr_created https://github.com/a/b/pull/1"}, // dup URL: one query
	}

	t.Run("gh answers", func(t *testing.T) {
		fakeGh(t, map[string]string{
			"https://github.com/a/b/pull/1": "MERGED",
			"https://github.com/a/b/pull/2": "OPEN",
		})
		states := PRStates(events, GhPRState)
		if states["https://github.com/a/b/pull/1"] != "MERGED" || states["https://github.com/a/b/pull/2"] != "OPEN" {
			t.Fatalf("states: %v", states)
		}
	})

	t.Run("gh fails → unknown, report still complete", func(t *testing.T) {
		states := PRStates(events, func(context.Context, string) (string, error) {
			return "", errors.New("no gh")
		})
		if states["https://github.com/a/b/pull/1"] != "UNKNOWN" {
			t.Fatalf("states: %v", states)
		}
		// and the aggregate falls back to feedback's pr_merged
		ev2 := append(append([]Event(nil), events...),
			Event{Kind: "feedback", Session: "s1", Time: now, Extra: "pr_merged https://github.com/a/b/pull/1"},
			ev("decide", "s1", at(time.Hour), func(e *Event) { e.Arm, e.Tier = "router", "fast" }),
			ev("decide", "s2", at(time.Hour), func(e *Event) { e.Arm, e.Tier = "router", "fast" }))
		rep := Aggregate(ReportInput{
			Events: ev2, Now: testNow, Since: 14 * 24 * time.Hour,
			PRStates: states,
		})
		// s1's PR counts merged via the fallback; s2's stays open
		if rep.Router.MergedSessions != 1 || rep.Router.PRSessions != 2 {
			t.Fatalf("merged %d of %d", rep.Router.MergedSessions, rep.Router.PRSessions)
		}
	})
}

func TestRenderTextAndJSON(t *testing.T) {
	rep := Aggregate(ReportInput{
		Events: []Event{
			ev("decide", "r1", at(time.Hour), func(e *Event) { e.Arm, e.Tier, e.Harness, e.Agent = "router", "fast", "codex", "main" }),
			ev("shadow", "c1", at(time.Hour), func(e *Event) { e.Arm, e.Tier, e.ShadowTier = "control", "performance", "fast" }),
			ev("feedback", "r1", at(30*time.Minute), func(e *Event) { e.Extra = "pr_created https://github.com/a/b/pull/1" }),
		},
		Records: []magpie.UsageRow{
			rec("r1", at(50*time.Minute), 1000, 1000, 0),
			rec("c1", at(50*time.Minute), 5000, 1000, 0),
		},
		Now: testNow, Since: 14 * 24 * time.Hour,
		PRStates: map[string]string{"https://github.com/a/b/pull/1": "MERGED"},
	})
	var text strings.Builder
	Render(&text, rep)
	for _, want := range []string{
		"sessions",
		"cost/session median",
		"tier distribution",
		"merged-PR sessions",
		"hint hit rate",
		"样本不足",
		"升档率",
		"未升档轮次的选低率",
	} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("render missing %q:\n%s", want, text.String())
		}
	}

	var jsonOut strings.Builder
	RenderJSON(&jsonOut, rep)
	if !strings.Contains(jsonOut.String(), `"cost_median"`) || !strings.Contains(jsonOut.String(), `"merged_z"`) {
		t.Fatalf("json:\n%s", jsonOut.String())
	}
}
