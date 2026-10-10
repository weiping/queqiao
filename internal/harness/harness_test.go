package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTurnPostsWithinBudget(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"tier":"fast","group":"group/mb-fast"}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}

	out, err := c.Turn(context.Background(), map[string]any{"prompt": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out["tier"] != "fast" {
		t.Fatalf("out: %v", out)
	}
	if got["prompt"] != "hi" {
		t.Fatalf("posted: %v", got)
	}
}

func TestTurnTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	start := time.Now()
	if _, err := c.Turn(context.Background(), map[string]any{}); err == nil {
		t.Fatal("no timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("turn waited past its budget")
	}
}

func TestTurnRejectsBadReplies(t *testing.T) {
	for name, body := range map[string]string{
		"http 500": `{"error":"down"}`,
		"no tier":  `{"group":"group/mb-fast"}`,
		"not json": `nope`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if name == "http 500" {
				w.WriteHeader(500)
			}
			io.WriteString(w, body)
		}))
		c := &Client{Base: srv.URL, HTTP: srv.Client()}
		if _, err := c.Turn(context.Background(), map[string]any{}); err == nil {
			t.Fatalf("%s: expected error", name)
		}
		srv.Close()
	}
}

func TestFeedbackAndLineageNeverErrorOutward(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // even a failing gateway stays silent
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	c.Feedback(context.Background(), map[string]any{"kind": "pr_created"})
	c.Lineage(context.Background(), map[string]any{"session": "s"})
}

// SP7 §3.5: Review posts to /review and never errors outward, even when
// the gateway is gone.
func TestReviewPostsAndSwallowsErrors(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bridge/review" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	c.Review(context.Background(), map[string]any{"session": "s1", "answer": "ok"})
	if got["session"] != "s1" || got["answer"] != "ok" {
		t.Fatalf("posted: %v", got)
	}

	down := &Client{Base: "http://127.0.0.1:1", HTTP: http.DefaultClient}
	done := make(chan struct{})
	go func() { defer close(done); down.Review(context.Background(), map[string]any{"session": "s1"}) }()
	select {
	case <-done:
	case <-time.After(2 * ReviewBudget):
		t.Fatal("Review did not return within its budget")
	}
}

func TestRunAlwaysExitsZero(t *testing.T) {
	for name, h := range map[string]Handler{
		"error":  func(context.Context, []byte, *Client) ([]byte, error) { return nil, io.ErrUnexpectedEOF },
		"panic":  func(context.Context, []byte, *Client) ([]byte, error) { panic("boom") },
		"silent": func(context.Context, []byte, *Client) ([]byte, error) { return nil, nil },
		"output": func(context.Context, []byte, *Client) ([]byte, error) { return []byte(`{"x":1}`), nil },
	} {
		if code := Run(h); code != 0 {
			t.Fatalf("%s: exit %d", name, code)
		}
	}
}

func TestHarnessDefaultURLIs3426(t *testing.T) {
	t.Setenv("MBRIDGE_URL", "")
	if got := NewClient().Base; got != "http://127.0.0.1:3426" {
		t.Fatalf("base %q", got)
	}
}

// SP10: the Codex hook waits as long as router.json lets a turn take.
func TestTurnWaitsTheConfiguredBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1800 * time.Millisecond)
		_, _ = w.Write([]byte(`{"tier":"fast","group":"group/mb-fast","reason":"R6-adopt"}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client(), TurnBudget: 3 * time.Second}
	out, err := c.Turn(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("a 1.8 s /turn within a 3 s budget failed: %v", err)
	}
	if out["tier"] != "fast" {
		t.Fatalf("out %v", out)
	}
}

func TestNewClientReadsTheBudgetFromRouterJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MBRIDGE_CONFIG_DIR", dir)
	t.Setenv("MBRIDGE_URL", "http://127.0.0.1:1") // no mbridge to ask: the file decides
	if c := NewClient(); c.budget() != Budget {
		t.Fatalf("no router.json: budget %v, want %v", c.budget(), Budget)
	}
	cfg := `{"version":1,"router_group":"mbridge","default_tier":"balanced","classifier":"typesafe/jev-latest",
	  "classify_timeout_ms":2500,
	  "tiers":{"fast":{"group":"mb-fast","claude_alias":"haiku","criteria":"f"},
	           "balanced":{"group":"mb-balanced","claude_alias":"sonnet","criteria":"b"},
	           "performance":{"group":"mb-perf","claude_alias":"opus","criteria":"p"}}}`
	if err := os.WriteFile(filepath.Join(dir, "router.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := NewClient(); c.budget() != 3*time.Second {
		t.Fatalf("classify 2500: budget %v, want 3s", c.budget())
	}
}

// SP10: Run's own cap on a hook never cuts a /turn short of its budget.
func TestRunOutlivesTheTurnBudget(t *testing.T) {
	for _, b := range []time.Duration{0, 1500 * time.Millisecond, 3 * time.Second, 8 * time.Second} {
		c := &Client{TurnBudget: b}
		if got := runTimeout(c); got <= c.budget() || got < 5*time.Second {
			t.Errorf("budget %v: Run allows %v", c.budget(), got)
		}
	}
}

// SP10 review: the hook asks the running mbridge first, so a daemon whose
// router.json differs from the one on disk (edited since, another config
// dir, MBRIDGE_URL elsewhere) still sets the budget.
func TestNewClientAsksMbridgeForTheBudget(t *testing.T) {
	t.Setenv("MBRIDGE_CONFIG_DIR", t.TempDir()) // no router.json on disk
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/bridge/router" {
			_, _ = w.Write([]byte(`{"turn_budget_ms":4000}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("MBRIDGE_URL", srv.URL)
	if c := NewClient(); c.budget() != 4*time.Second {
		t.Fatalf("budget %v, want mbridge's 4s", c.budget())
	}
	// an answer outside [1500, 8000] is held to it
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"turn_budget_ms":60000}`))
	})
	if c := NewClient(); c.budget() != 8*time.Second {
		t.Fatalf("budget %v, want 8s", c.budget())
	}
}

// A hung mbridge costs the hook at most the probe's 300 ms.
func TestNewClientProbeGivesUpQuickly(t *testing.T) {
	t.Setenv("MBRIDGE_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	t.Setenv("MBRIDGE_URL", srv.URL)
	start := time.Now()
	c := NewClient()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("NewClient waited %v on a hung mbridge", took)
	}
	if c.budget() != Budget {
		t.Fatalf("budget %v, want the 1500 ms fallback", c.budget())
	}
}
