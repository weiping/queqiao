package router

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// emptyJSON is a valid config with the review section left out, so the
// tests exercise the defaults.
const emptyJSON = `{"tiers":{"fast":{"group":"g","claude_alias":"f"},"balanced":{"group":"g","claude_alias":"b"},"performance":{"group":"g","claude_alias":"p"}}}`

// syncEvents is a goroutine-safe Deps.Log: reviews are logged from the
// background goroutine while the request goroutine logs decisions.
type syncEvents struct {
	mu sync.Mutex
	ev []Event
}

func (s *syncEvents) log(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ev = append(s.ev, ev)
	return nil
}

func (s *syncEvents) snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.ev))
	copy(out, s.ev)
	return out
}

// reviewDeps builds Deps in the given review mode over the fake classifier.
func reviewDeps(t *testing.T, mode string, verdict *Verdict) (*Deps, *fakeClassifier, *syncEvents) {
	t.Helper()
	cfg, err := Load(writeGlobal(t, emptyJSON), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Review.Mode = mode
	events := &syncEvents{}
	fc := &fakeClassifier{verdict: verdict, reviewDone: make(chan struct{}, 8)}
	d := &Deps{Config: cfg, Sessions: NewSessions(), Hints: NewHints(), Classify: fc, Log: events.log}
	return d, fc, events
}

func reviewBody(session string) map[string]any {
	return map[string]any{"session": session, "harness": "claude-code", "turn_id": "t1",
		"prompt": "please fix it", "answer": "I could not"}
}

// postReview posts one review and returns the status code.
func postReview(t *testing.T, url string, body any) int {
	t.Helper()
	res := postJSON(t, url, body)
	defer res.Body.Close()
	return res.StatusCode
}

// turn posts one turn and returns its decoded reply.
func turn(t *testing.T, url, session, prompt string) map[string]any {
	t.Helper()
	res := postJSON(t, url, map[string]any{"session": session, "prompt": prompt, "agent": "main"})
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return out
}

func TestReviewAcceptedThenEscalates(t *testing.T) {
	d, fc, events := reviewDeps(t, ReviewAct, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	fc.review = &ReviewVerdict{Unresolved: 0.9, Confidence: 0.8}
	ts := testServer(d)
	defer ts.Close()

	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusAccepted {
		t.Fatalf("review status = %d, want 202", code)
	}
	<-fc.reviewDone // the background review finished

	got := turn(t, ts.URL+"/v1/bridge/turn", "s1", "still broken")
	if got["reason"] != "R3-review" || got["tier"] != "balanced" {
		t.Fatalf("second turn: %+v, want R3-review → balanced", got)
	}
	n := 0
	for _, ev := range events.snapshot() {
		if ev.Kind == "review" {
			n++
			if ev.TurnID != "t1" || ev.Unresolved == nil || *ev.Unresolved != 0.9 {
				t.Fatalf("review event: %+v", ev)
			}
		}
	}
	if n != 1 {
		t.Fatalf("review events = %d, want 1", n)
	}
}

func TestReviewShadowLogsWouldReview(t *testing.T) {
	d, fc, events := reviewDeps(t, ReviewShadow, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	fc.review = &ReviewVerdict{Unresolved: 0.9, Confidence: 0.8}
	ts := testServer(d)
	defer ts.Close()

	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1"))
	<-fc.reviewDone

	got := turn(t, ts.URL+"/v1/bridge/turn", "s1", "still broken")
	if got["reason"] == "R3-review" {
		t.Fatalf("shadow mode escalated: %+v", got)
	}
	all := events.snapshot()
	last := all[len(all)-1]
	if !last.WouldReview {
		t.Fatalf("would_review not logged: %+v", last)
	}
}

func TestReviewOffIs204(t *testing.T) {
	d, fc, _ := reviewDeps(t, ReviewOff, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	ts := testServer(d)
	defer ts.Close()
	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusNoContent {
		t.Fatalf("off mode status = %d, want 204", code)
	}
	if fc.reviewCalls() != 0 {
		t.Fatal("off mode still called Review")
	}
}

func TestReviewPerformanceIs204(t *testing.T) {
	d, _, _ := reviewDeps(t, ReviewAct, &Verdict{Tier: TierPerformance, TierConfidence: 0.9})
	ts := testServer(d)
	defer ts.Close()
	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusNoContent {
		t.Fatalf("performance tier status = %d, want 204", code)
	}
}

func TestReviewControlArmIs204(t *testing.T) {
	d, _, _ := reviewDeps(t, ReviewAct, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	d.Config.Experiment = ExperimentConfig{Enabled: true, RouterPercent: 0, ControlTier: TierPerformance, Salt: "s"}
	ts := testServer(d)
	defer ts.Close()
	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusNoContent {
		t.Fatalf("control arm status = %d, want 204", code)
	}
}

func TestPinnedSessionIsNotReviewed(t *testing.T) {
	d, fc, _ := reviewDeps(t, ReviewAct, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	fc.review = &ReviewVerdict{Unresolved: 0.9, Confidence: 0.8}
	ts := testServer(d)
	defer ts.Close()
	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	res := postJSON(t, ts.URL+"/v1/bridge/feedback", map[string]any{"session": "s1", "kind": "manual_model_switch"})
	res.Body.Close()
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusNoContent {
		t.Fatalf("pinned status = %d, want 204", code)
	}
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusNoContent {
		t.Fatalf("pinned status on retry = %d, want 204", code)
	}
}

func TestReviewMissingAnswerIs400(t *testing.T) {
	d, _, _ := reviewDeps(t, ReviewAct, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	ts := testServer(d)
	defer ts.Close()
	body := reviewBody("s1")
	delete(body, "answer")
	if code := postReview(t, ts.URL+"/v1/bridge/review", body); code != http.StatusBadRequest {
		t.Fatalf("missing answer status = %d, want 400", code)
	}
}

func TestLateReviewIsDropped(t *testing.T) {
	d, fc, _ := reviewDeps(t, ReviewAct, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	fc.review = &ReviewVerdict{Unresolved: 0.9, Confidence: 0.8}
	fc.gate = make(chan struct{})
	ts := testServer(d)
	defer ts.Close()

	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	if code := postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1")); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	start := time.Now()
	got := turn(t, ts.URL+"/v1/bridge/turn", "s1", "still broken")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("a turn waited %v for an in-flight review", elapsed)
	}
	if got["reason"] == "R3-review" {
		t.Fatalf("unfinished review escalated: %+v", got)
	}
	close(fc.gate) // let the late result land
	<-fc.reviewDone
	got = turn(t, ts.URL+"/v1/bridge/turn", "s1", "and again")
	if got["reason"] == "R3-review" {
		t.Fatalf("late review was not dropped: %+v", got)
	}
}

func TestReviewIsReadOnce(t *testing.T) {
	d, fc, _ := reviewDeps(t, ReviewAct, &Verdict{Tier: TierFast, TierConfidence: 0.9})
	fc.review = &ReviewVerdict{Unresolved: 0.9, Confidence: 0.8}
	ts := testServer(d)
	defer ts.Close()
	turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
	postReview(t, ts.URL+"/v1/bridge/review", reviewBody("s1"))
	<-fc.reviewDone
	if got := turn(t, ts.URL+"/v1/bridge/turn", "s1", "still broken"); got["reason"] != "R3-review" {
		t.Fatalf("second turn: %+v", got)
	}
	if got := turn(t, ts.URL+"/v1/bridge/turn", "s1", "and again"); got["reason"] != "R4-escalation-hold" {
		t.Fatalf("third turn reused the review: %+v", got)
	}
}

func TestTruncateAnswer(t *testing.T) {
	s := strings.Repeat("a", 7000) + "Z"
	out := truncateAnswer(s, 6000)
	if len([]rune(out)) != 6000 {
		t.Fatalf("len = %d, want 6000", len([]rune(out)))
	}
	if !strings.HasPrefix(out, strings.Repeat("a", 2000)) {
		t.Fatal("the first 2000 runes were not kept")
	}
	if !strings.HasSuffix(out, "Z") {
		t.Fatal("the last rune was not kept")
	}
}

func TestJevReviewRequestShape(t *testing.T) {
	cfg := testConfig(jevClassifier)
	var bodies []string
	f := askFunc(func(_ context.Context, _, body string) (string, error) {
		bodies = append(bodies, body)
		return `{"answers":{"unresolved":{"type":"noul","noul":0.7}}}`, nil
	})
	v, err := NewClassifier(cfg, f.ask).Review(context.Background(), ReviewQuestion{
		Request: "fix it", Answer: "no", ToolCalls: 7, ToolFailures: 1, Tier: TierFast})
	if err != nil {
		t.Fatal(err)
	}
	if v.Unresolved != 0.7 {
		t.Fatalf("verdict: %+v", v)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	state := req["state"].(map[string]any)
	if state["answer"] != "no" || state["tool_calls"].(float64) != 7 || state["tier"] != "fast" {
		t.Fatalf("state: %+v", state)
	}
	q := req["questions"].(map[string]any)["unresolved"].(map[string]any)
	if q["type"] != "noul" {
		t.Fatalf("question: %+v", q)
	}
}

func TestTurnLatencyUnaffectedByReview(t *testing.T) {
	measure := func(mode string) time.Duration {
		d, fc, _ := reviewDeps(t, mode, &Verdict{Tier: TierFast, TierConfidence: 0.9})
		fc.review = &ReviewVerdict{Unresolved: 0.9, Confidence: 0.8}
		fc.reviewSleep = 2 * time.Second
		ts := testServer(d)
		defer ts.Close()
		lat := make([]time.Duration, 0, 50)
		for i := 0; i < 50; i++ {
			start := time.Now()
			turn(t, ts.URL+"/v1/bridge/turn", "s1", "fix it")
			lat = append(lat, time.Since(start))
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		return lat[len(lat)*95/100] // p95
	}
	act, off := measure(ReviewAct), measure(ReviewOff)
	if act-off > 50*time.Millisecond {
		t.Fatalf("act mode turns waited longer than off: act=%v off=%v", act, off)
	}
}
