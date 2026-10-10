// Package testmagpie runs an official magpie binary for queqiao's contract
// and end-to-end tests (SP8 Task 14): an isolated HOME, a gateway on a free
// loopback port (MAGPIE_ADDR), and a fake OpenAI-compatible upstream
// registered as the custom provider "fake". Tests that import it skip
// unless -magpie (or QUEQIAO_TEST_MAGPIE) names the binary.
package testmagpie

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var binFlag = flag.String("magpie", os.Getenv("QUEQIAO_TEST_MAGPIE"), "official magpie binary for contract and e2e tests")

// Bin is the magpie binary under test; the test skips without one.
func Bin(t *testing.T) string {
	t.Helper()
	if *binFlag == "" {
		t.Skip("no magpie binary: pass -magpie=<path> or set QUEQIAO_TEST_MAGPIE")
	}
	p, err := filepath.Abs(*binFlag)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Call is one request the fake upstream received.
type Call struct {
	Model   string
	Stream  bool
	Content string // the last message's text
}

// Upstream is a fake OpenAI-compatible vendor. Answer decides the reply
// text for a request (default "ok").
type Upstream struct {
	*httptest.Server
	mu     sync.Mutex
	calls  []Call
	Answer func(c Call) string
}

// NewUpstream starts one, closed with the test.
func NewUpstream(t *testing.T) *Upstream {
	u := &Upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(u.Close)
	return u
}

// Calls is what it has received so far.
func (u *Upstream) Calls() []Call {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Call(nil), u.calls...)
}

// Models counts the received calls by model.
func (u *Upstream) Models() map[string]int {
	out := map[string]int{}
	for _, c := range u.Calls() {
		out[c.Model]++
	}
	return out
}

func (u *Upstream) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[]}`)
		return
	}
	var req struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &req)
	c := Call{Model: req.Model, Stream: req.Stream}
	if n := len(req.Messages); n > 0 {
		c.Content = text(req.Messages[n-1].Content)
	}
	u.mu.Lock()
	u.calls = append(u.calls, c)
	answer := u.Answer
	u.mu.Unlock()
	reply := "ok"
	if answer != nil {
		reply = answer(c)
	}
	rj, _ := json.Marshal(reply)
	if c.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%s}}]}\n\n", c.Model, rj)
		fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n", c.Model)
		io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"x","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`, c.Model, rj)
}

// text is a message content's text: a string, or the text parts of a list.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &parts)
	var out []string
	for _, p := range parts {
		out = append(out, p.Text)
	}
	return strings.Join(out, "\n")
}

// Magpie is a running official magpie gateway.
type Magpie struct {
	Bin  string
	Home string
	URL  string // http://127.0.0.1:<port>
	Env  []string
}

// Start runs magpie serve in a fresh HOME with up registered as the
// provider "fake" serving models, and waits for its gateway. It stops
// with the test.
func Start(t *testing.T, bin string, up *Upstream, models ...string) *Magpie {
	t.Helper()
	home := t.TempDir()
	addr := freeAddr(t)
	m := &Magpie{Bin: bin, Home: home, URL: "http://" + addr}
	m.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME=", "XDG_CACHE_HOME=", "MAGPIE_ADDR="+addr)
	if up != nil {
		m.CLI(t, "provider", "add", "fake", "url="+up.URL+"/v1", "key=k", "models="+strings.Join(models, ","))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "serve")
	cmd.Env = m.Env
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("magpie serve did not stop")
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		res, err := http.Get(m.URL + "/v1/models")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("magpie gateway on %s never answered: %v\n%s", addr, err, log.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// CLI runs magpie with args in this HOME, failing the test on an error.
func (m *Magpie) CLI(t *testing.T, args ...string) string {
	t.Helper()
	out, err := m.Run(args...)
	if err != nil {
		t.Fatalf("magpie %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// Run runs magpie with args in this HOME.
func (m *Magpie) Run(args ...string) (string, error) {
	cmd := exec.Command(m.Bin, args...)
	cmd.Env = m.Env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Runner adapts Run to magpie.Client's Run hook, for this HOME.
func (m *Magpie) Runner() func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = m.Env
		return cmd.CombinedOutput()
	}
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}
