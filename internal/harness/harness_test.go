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
		w.Write([]byte(`{"tier":"fast","group":"group/qq-fast"}`))
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
		"no tier":  `{"group":"group/qq-fast"}`,
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
