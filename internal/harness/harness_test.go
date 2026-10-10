package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
