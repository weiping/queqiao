package router

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weiping/queqiao/internal/wire"
)

func TestSessionCommitGet(t *testing.T) {
	s := NewSessions()
	if got := s.Get("sess"); got != nil {
		t.Fatalf("first turn: %+v", got)
	}
	s.Commit("sess", TurnState{Tier: TierBalanced})
	got := s.Get("sess")
	if got == nil || got.Tier != TierBalanced {
		t.Fatalf("after commit: %+v", got)
	}
}

func TestSessionObserveCountsTools(t *testing.T) {
	s := NewSessions()
	s.Observe("sess", wire.ToolStats{Calls: 2, Failures: 1})
	calls, failures := s.Stats("sess")
	if calls != 2 || failures != 1 {
		t.Fatalf("calls %d failures %d", calls, failures)
	}
	if s.SinceLast("sess") < 0 {
		t.Fatal("SinceLast negative")
	}
}

func TestSessionEviction(t *testing.T) {
	s := NewSessions()
	base := time.Now()
	sessionClock = func() time.Time { return base }
	t.Cleanup(func() { sessionClock = time.Now })
	s.Commit("old", TurnState{Tier: TierFast})
	// fill past the eviction threshold so evictLocked scans
	for i := 0; i < 1100; i++ {
		s.Commit(fmt.Sprintf("filler-%d", i), TurnState{Tier: TierFast})
	}
	sessionClock = func() time.Time { return base.Add(25 * time.Hour) }
	s.Commit("new", TurnState{Tier: TierFast})
	if got := s.Get("old"); got != nil {
		t.Fatalf("old session survived eviction: %+v", got)
	}
	if got := s.Get("new"); got == nil {
		t.Fatal("new session evicted")
	}
}

func TestDerivedSessions(t *testing.T) {
	s := NewSessions()
	s.Commit("parent", TurnState{Tier: TierPerformance, EscalatedLeft: 1, LowerStreak: 1})
	s.MarkDerived("child", "parent")
	if p, ok := s.ParentOf("child", ""); !ok || p != "parent" {
		t.Fatalf("ParentOf: %q %v", p, ok)
	}
	got := s.InheritFrom("child", "parent")
	if got == nil || got.Tier != TierPerformance || got.EscalatedLeft != 1 || got.LowerStreak != 0 {
		t.Fatalf("inherit: %+v", got)
	}
	if c := s.Get("child"); c == nil || c.Tier != TierPerformance {
		t.Fatalf("child state: %+v", c)
	}
	if _, ok := s.ParentOf("unknown", "words"); ok {
		t.Fatal("unknown session has a parent")
	}
}

func TestHintRoundTrip(t *testing.T) {
	h := NewHints()
	key := HintKey{Session: "s", TurnID: "t1", PromptHash: "ph"}
	h.Put(Hint{Key: key, Tier: TierFast})
	got, ok := h.Take(HintKey{Session: "s", TurnID: "t1"})
	if !ok || got.Tier != TierFast {
		t.Fatalf("take: %+v %v", got, ok)
	}
	if _, ok := h.Take(HintKey{Session: "s", TurnID: "t1"}); ok {
		t.Fatal("take is not once")
	}
}

func TestHintMatchOrder(t *testing.T) {
	h := NewHints()
	// stored with turn id and hash
	h.Put(Hint{Key: HintKey{Session: "s", TurnID: "t9", PromptHash: "ph9"}, Tier: TierFast})
	// (b): session + hash matches when turn id differs
	got, ok := h.Take(HintKey{Session: "s", TurnID: "other", PromptHash: "ph9"})
	if !ok || got.Tier != TierFast {
		t.Fatalf("(b) match: %+v %v", got, ok)
	}
	// (c): hash alone matches across sessions
	h.Put(Hint{Key: HintKey{Session: "s1", PromptHash: "shared"}, Tier: TierBalanced})
	got, ok = h.Take(HintKey{Session: "s2", PromptHash: "shared"})
	if !ok || got.Tier != TierBalanced {
		t.Fatalf("(c) match: %+v %v", got, ok)
	}
}

func TestHintTTL(t *testing.T) {
	h := NewHints()
	base := time.Now()
	hintClock = func() time.Time { return base }
	t.Cleanup(func() { hintClock = time.Now })
	h.Put(Hint{Key: HintKey{Session: "s", TurnID: "t"}, Tier: TierFast})
	hintClock = func() time.Time { return base.Add(121 * time.Second) }
	if _, ok := h.Take(HintKey{Session: "s", TurnID: "t"}); ok {
		t.Fatal("expired hint taken")
	}
}

func TestArmDeterministic(t *testing.T) {
	e := ExperimentConfig{Enabled: true, RouterPercent: 50, Salt: "salt"}
	if Arm("sess", e) != Arm("sess", e) {
		t.Fatal("Arm not deterministic")
	}
	if Arm("sess", ExperimentConfig{Enabled: false}) != "router" {
		t.Fatal("disabled experiment must route")
	}
	e.RouterPercent = 0
	if Arm("anything", e) != "control" {
		t.Fatal("0% router should be control")
	}
	e.RouterPercent = 100
	if Arm("anything", e) != "router" {
		t.Fatal("100% router should be router")
	}
}

func TestArmSplits(t *testing.T) {
	e := ExperimentConfig{Enabled: true, RouterPercent: 50, Salt: "salt"}
	router, control := 0, 0
	for i := 0; i < 1000; i++ {
		if Arm("session-"+string(rune(i%128))+"-"+string(rune(i/128)), e) == "router" {
			router++
		} else {
			control++
		}
	}
	if router < 300 || router > 700 {
		t.Fatalf("split too skewed: %d router / %d control", router, control)
	}
}

func TestAppendWritesLine(t *testing.T) {
	p := t.TempDir() + "/router.jsonl"
	SetEventsPath(p)
	t.Cleanup(func() { SetEventsPath("") })
	if err := Append(Event{Kind: "decide", Session: "s", Tier: TierBalanced, Reason: "R6-adopt"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(Event{Kind: "feedback", Session: "s", Extra: "pr_created"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines: %d", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["kind"] != "decide" || first["tier"] != "balanced" || first["t"] == "" {
		t.Fatalf("first line: %v", first)
	}
}

// SinceLast runs from whichever came last: a decision (Commit) or a
// request the proxy saw (Observe).
func TestSinceLastUsesLatestOfDecideAndObserve(t *testing.T) {
	s := NewSessions()
	base := time.Now()
	t.Cleanup(func() { sessionClock = time.Now })
	sessionClock = func() time.Time { return base }
	s.Commit("sess", TurnState{Tier: TierFast})
	sessionClock = func() time.Time { return base.Add(time.Minute) }
	s.Observe("sess", wire.ToolStats{})
	sessionClock = func() time.Time { return base.Add(3 * time.Minute) }
	if got := s.SinceLast("sess"); got != 2*time.Minute {
		t.Fatalf("after observe: %v", got)
	}
	s.Commit("sess", TurnState{Tier: TierFast})
	sessionClock = func() time.Time { return base.Add(4 * time.Minute) }
	if got := s.SinceLast("sess"); got != time.Minute {
		t.Fatalf("after commit: %v", got)
	}
}
