package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

/**
 * The common runtime of mbridge's command hooks (§6.6): read the event
 * JSON from stdin, call the gateway within a budget, and always end with
 * exit code 0 — a hook failure must never disturb the agent.
 *
 * The Codex input parsing and output shapes live in harness/codex; this
 * package stays harness-agnostic.
 */

// Budget is the HTTP budget a /turn call gets, matching §6.6/§6.9.
const Budget = 1500 * time.Millisecond

// ReviewBudget is the budget an end-of-turn review gets (SP7 §3.5): the
// answer is already delivered, so this is the only wait it may cost.
const ReviewBudget = time.Second

// Client talks to the mbridge gateway from a hook process.
type Client struct {
	Base string
	HTTP *http.Client
}

// NewClient points at MBRIDGE_URL, or the default gateway on loopback.
func NewClient() *Client {
	base := os.Getenv("MBRIDGE_URL")
	if base == "" {
		base = "http://127.0.0.1:3426"
	}
	return &Client{Base: base, HTTP: http.DefaultClient}
}

// Turn posts a /turn request and parses the reply. The ctx or the budget
// bounds the wait, whichever ends first.
func (c *Client) Turn(ctx context.Context, body map[string]any) (map[string]any, error) {
	tctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	var out map[string]any
	if err := c.post(tctx, "/v1/bridge/turn", body, &out); err != nil {
		return nil, err
	}
	if s, _ := out["tier"].(string); s == "" {
		return nil, fmt.Errorf("mbridge: /turn gave no tier")
	}
	return out, nil
}

// Feedback reports an event; failures are swallowed (best effort).
func (c *Client) Feedback(ctx context.Context, body map[string]any) {
	tctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	_ = c.post(tctx, "/v1/bridge/feedback", body, nil)
}

// Review posts an end-of-turn review; errors are swallowed (SP7 §3.5).
func (c *Client) Review(ctx context.Context, body map[string]any) {
	tctx, cancel := context.WithTimeout(ctx, ReviewBudget)
	defer cancel()
	_ = c.post(tctx, "/v1/bridge/review", body, nil)
}

// Lineage marks a derived session; best effort like Feedback.
func (c *Client) Lineage(ctx context.Context, body map[string]any) {
	tctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	_ = c.post(tctx, "/v1/bridge/lineage", body, nil)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("mbridge: %s: %d %s", path, res.StatusCode, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

// MaxStdin caps what a hook reads; Codex transcripts can be big but the
// fields the handlers need sit well below this.
const MaxStdin = 1 << 20

// Handler processes one hook invocation. It returns the bytes to write to
// stdout (nil for none); any error or panic is logged to stderr and the
// process still exits 0.
type Handler func(ctx context.Context, stdin []byte, c *Client) ([]byte, error)

// Run reads stdin, runs the handler and returns the process exit code —
// always 0, whatever happened. stdout stays empty unless the handler
// returned bytes.
func Run(h Handler) int {
	stdin, _ := io.ReadAll(io.LimitReader(os.Stdin, MaxStdin))
	c := NewClient()
	var out []byte
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "mbridge hook: panic: %v\n", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b, err := h(ctx, stdin, c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mbridge hook: %v\n", err)
			return
		}
		out = b
	}()
	if len(out) > 0 {
		os.Stdout.Write(out)
	}
	return 0
}
