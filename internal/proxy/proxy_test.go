package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weiping/queqiao/internal/router"
)

const routerJSON = `{
  "version": 1,
  "router_group": "queqiao",
  "tiers": {
    "fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "fast criteria" },
    "balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "balanced criteria" },
    "performance": { "group": "qq-perf",     "claude_alias": "opus",   "criteria": "perf criteria" }
  },
  "default_tier": "balanced",
  "classifier": "typesafe/jev-latest",
  "classify_timeout_ms": 1500
}`

// scripted is a classifier that answers tiers in order, counting asks.
type scripted struct {
	mu    sync.Mutex
	tiers []router.Tier
	asks  int
}

func (s *scripted) Classify(_ context.Context, q router.Question) (*router.Verdict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tiers[min(s.asks, len(s.tiers)-1)]
	s.asks++
	return &router.Verdict{Tier: t, TierConfidence: 0.9, Source: "typesafe/jev-latest"}, nil
}

func (s *scripted) Review(context.Context, router.ReviewQuestion) (*router.ReviewVerdict, error) {
	return nil, io.EOF
}

// upstream is a fake magpie recording what reached it.
type upstream struct {
	mu      sync.Mutex
	models  []string
	bodies  [][]byte
	headers []http.Header
	handle  func(w http.ResponseWriter, r *http.Request)
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var q struct {
		Model string `json:"model"`
	}
	json.Unmarshal(b, &q)
	u.mu.Lock()
	u.models, u.bodies, u.headers = append(u.models, q.Model), append(u.bodies, b), append(u.headers, r.Header.Clone())
	u.mu.Unlock()
	if u.handle != nil {
		u.handle(w, r)
		return
	}
	io.WriteString(w, `{"ok":true}`)
}

type env struct {
	up     *upstream
	srv    *httptest.Server
	cls    *scripted
	events *[]router.Event
	deps   *router.Deps
}

func setup(t *testing.T, tiers ...router.Tier) *env {
	t.Helper()
	p := filepath.Join(t.TempDir(), "router.json")
	os.WriteFile(p, []byte(routerJSON), 0o644)
	cfg, err := router.Load(p, "")
	if err != nil {
		t.Fatal(err)
	}
	up := &upstream{}
	mag := httptest.NewServer(up)
	t.Cleanup(mag.Close)
	target, _ := url.Parse(mag.URL)
	if len(tiers) == 0 {
		tiers = []router.Tier{router.TierFast}
	}
	cls := &scripted{tiers: tiers}
	var events []router.Event
	var mu sync.Mutex
	deps := &router.Deps{Config: cfg, Classify: cls, Log: func(ev router.Event) error {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		return nil
	}}
	srv := httptest.NewServer(Handler(target, deps))
	t.Cleanup(srv.Close)
	return &env{up: up, srv: srv, cls: cls, events: &events, deps: deps}
}

func (e *env) post(t *testing.T, path string, hdr map[string]string, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) do(t *testing.T, path string, hdr map[string]string, body string) {
	t.Helper()
	res := e.post(t, path, hdr, body)
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
}

func responses(text string, withResult bool) string {
	items := []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}
	if withResult {
		items = append(items,
			map[string]any{"type": "function_call", "call_id": "c1", "name": "exec_command", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"})
	}
	b, _ := json.Marshal(map[string]any{"model": "group/queqiao", "stream": true, "input": items})
	return string(b)
}

var codexHdr = map[string]string{"session-id": "sess-1", "User-Agent": "codex_cli_rs/0.162.1", "originator": "codex_exec"}

func TestProxyStreamsChunksAsTheyCome(t *testing.T) {
	e := setup(t)
	release := make(chan struct{})
	e.up.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		io.WriteString(w, "data: two\n\n")
	}
	res := e.post(t, "/v1/responses", codexHdr, responses("hi", false))
	defer res.Body.Close()
	br := bufio.NewReader(res.Body)
	got := make(chan string, 1)
	go func() { line, _ := br.ReadString('\n'); got <- line }()
	select {
	case line := <-got:
		if line != "data: one\n" {
			t.Fatalf("first line %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk held back until the stream ended")
	}
	close(release)
	rest, _ := io.ReadAll(br)
	if !strings.Contains(string(rest), "data: two") {
		t.Fatalf("rest %q", rest)
	}
}

func TestProxyRewritesOnlyTopLevelModel(t *testing.T) {
	e := setup(t, router.TierPerformance)
	body := "\xef\xbb\xbf" + `{"metadata":{"model":"group/queqiao"},"model" : "group/queqiao","stream":true,` +
		`"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"redesign it"}]}]}`
	e.do(t, "/v1/responses", codexHdr, body)
	got := string(e.up.bodies[0])
	want := strings.Replace(body, `"model" : "group/queqiao"`, `"model" : "group/qq-perf"`, 1)
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestRewriteModel(t *testing.T) {
	out, ok := RewriteModel([]byte(`{"a":{"model":"x"},"model":"x","b":"model"}`), "y")
	if !ok || string(out) != `{"a":{"model":"x"},"model":"y","b":"model"}` {
		t.Fatalf("%s %v", out, ok)
	}
	if _, ok := RewriteModel([]byte(`{"a":1}`), "y"); ok {
		t.Fatal("rewrote a body with no model")
	}
	if _, ok := RewriteModel([]byte(`{"model":`), "y"); ok {
		t.Fatal("rewrote a truncated body")
	}
}

func TestProxySameTurnSameTier(t *testing.T) {
	e := setup(t, router.TierFast, router.TierPerformance)
	e.do(t, "/v1/responses", codexHdr, responses("look at the logs", false))
	e.do(t, "/v1/responses", codexHdr, responses("look at the logs", true))
	if e.up.models[0] != "group/qq-fast" || e.up.models[1] != "group/qq-fast" {
		t.Fatalf("models %v", e.up.models)
	}
	if e.cls.asks != 1 {
		t.Fatalf("classified %d times in one turn", e.cls.asks)
	}
}

func TestProxyNewTurnDecidesAgain(t *testing.T) {
	e := setup(t, router.TierFast, router.TierPerformance)
	e.do(t, "/v1/responses", codexHdr, responses("look at the logs", false))
	two, _ := json.Marshal(map[string]any{"model": "group/queqiao", "input": []any{
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "look at the logs"}}},
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "now redesign the cache"}}},
	}})
	e.do(t, "/v1/responses", codexHdr, string(two))
	if e.cls.asks != 2 {
		t.Fatalf("asks %d", e.cls.asks)
	}
	if e.up.models[1] == e.up.models[0] && e.up.models[1] != "group/qq-perf" {
		t.Fatalf("models %v", e.up.models)
	}
}

func TestProxyHintOrder(t *testing.T) {
	prompt := "fix the flaky test"
	hash := router.PromptHash(prompt)
	cases := []struct {
		name string
		key  router.HintKey
		hdr  map[string]string
	}{
		{"session+turn", router.HintKey{Session: "sess-1", TurnID: "t-1"}, map[string]string{"x-codex-turn-metadata": `{"turn_id":"t-1"}`}},
		{"session+hash", router.HintKey{Session: "sess-1", PromptHash: hash}, nil},
		{"hash only", router.HintKey{Session: "elsewhere", PromptHash: hash}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, router.TierFast)
			e.deps.Hints.Put(router.Hint{Key: c.key, Tier: router.TierPerformance})
			hdr := map[string]string{}
			for k, v := range codexHdr {
				hdr[k] = v
			}
			for k, v := range c.hdr {
				hdr[k] = v
			}
			e.do(t, "/v1/responses", hdr, responses(prompt, false))
			if e.up.models[0] != "group/qq-perf" || e.cls.asks != 0 {
				t.Fatalf("model %v asks %d", e.up.models, e.cls.asks)
			}
		})
	}
}

func TestProxyPassesOtherModelsUntouched(t *testing.T) {
	e := setup(t)
	body := `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	e.do(t, "/v1/chat/completions", nil, body)
	e.do(t, "/v1/models", nil, "")
	if string(e.up.bodies[0]) != body || e.cls.asks != 0 {
		t.Fatalf("body %s asks %d", e.up.bodies[0], e.cls.asks)
	}
}

func TestProxyOversizeBodyPassesThrough(t *testing.T) {
	e := setup(t)
	pad := strings.Repeat("x", MaxBody+1)
	body := `{"model":"group/queqiao","pad":"` + pad + `"}`
	e.do(t, "/v1/responses", codexHdr, body)
	if e.up.models[0] != "group/queqiao" || len(e.up.bodies[0]) != len(body) {
		t.Fatalf("model %q len %d", e.up.models[0], len(e.up.bodies[0]))
	}
	found := false
	for _, ev := range *e.events {
		if ev.Kind == "proxy_passthrough" {
			found = true
		}
	}
	if !found {
		t.Fatal("no proxy_passthrough event")
	}
}

func TestProxyMagpieDownIs502WithAddress(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	target, _ := url.Parse("http://" + addr)
	srv := httptest.NewServer(Handler(target, nil))
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 502 || !strings.Contains(string(b), addr) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestProxyKeepsAuthAndActorHeaders(t *testing.T) {
	e := setup(t)
	e.do(t, "/v1/responses", map[string]string{"Authorization": "Bearer magpie", "x-openai-actor-authorization": "magpie",
		"X-Magpie-Session": "s9"}, responses("hi", false))
	h := e.up.headers[0]
	if h.Get("Authorization") != "Bearer magpie" || h.Get("x-openai-actor-authorization") != "magpie" || h.Get("X-Magpie-Session") != "s9" {
		t.Fatalf("headers %v", h)
	}
}

func TestProxyTellsCodexFromOtherAgents(t *testing.T) {
	e := setup(t)
	e.do(t, "/v1/responses", codexHdr, responses("hi", false))
	e.do(t, "/v1/responses", map[string]string{"x-session-id": "oc-1", "User-Agent": "opencode/1.0"}, responses("hello there", false))
	var harnesses []string
	for _, ev := range *e.events {
		if ev.Kind == "decide" {
			harnesses = append(harnesses, ev.Harness)
		}
	}
	if strings.Join(harnesses, ",") != "codex,gateway" {
		t.Fatalf("harnesses %v", harnesses)
	}
}

func TestServeBadConfigPassesThrough(t *testing.T) {
	up := &upstream{}
	mag := httptest.NewServer(up)
	defer mag.Close()
	target, _ := url.Parse(mag.URL)
	router.SetConfigError(io.ErrUnexpectedEOF)
	defer router.SetConfigError(nil)
	srv := httptest.NewServer(Handler(target, nil))
	defer srv.Close()
	res, _ := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(responses("hi", false)))
	res.Body.Close()
	if up.models[0] != "group/queqiao" {
		t.Fatalf("rewritten without a config: %v", up.models)
	}
	res, _ = http.Get(srv.URL + "/v1/queqiao/router")
	b, _ := io.ReadAll(res.Body)
	if !bytes.Contains(b, []byte("unexpected EOF")) {
		t.Fatalf("status %s", b)
	}
}

func TestServePortInUseExplains(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	err := Serve(context.Background(), ln.Addr().String(), http.NotFoundHandler())
	if err == nil || !strings.Contains(err.Error(), ln.Addr().String()) || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("err = %v", err)
	}
}

// The proxy's added wait before the first byte of a stream, at p95 over
// loopback, stays under 5 ms (SP8 spec §3.2 item 5).
func TestProxyFirstByteOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	e := setup(t)
	e.up.handle = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
	}
	mag := httptest.NewServer(e.up)
	defer mag.Close()
	first := func(base string, hdr map[string]string, body string) time.Duration {
		req, _ := http.NewRequest("POST", base+"/v1/responses", strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		start := time.Now()
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		bufio.NewReader(res.Body).ReadString('\n')
		d := time.Since(start)
		res.Body.Close()
		return d
	}
	p95 := func(ds []time.Duration) time.Duration {
		slices := append([]time.Duration(nil), ds...)
		for i := range slices {
			for j := i + 1; j < len(slices); j++ {
				if slices[j] < slices[i] {
					slices[i], slices[j] = slices[j], slices[i]
				}
			}
		}
		return slices[len(slices)*95/100]
	}
	var direct, proxied []time.Duration
	for i := 0; i < 300; i++ {
		body := responses(fmt.Sprintf("turn %d", i), false)
		direct = append(direct, first(mag.URL, codexHdr, strings.Replace(body, "group/queqiao", "group/qq-fast", 1)))
		hdr := map[string]string{"session-id": fmt.Sprintf("s-%d", i), "User-Agent": "codex"}
		proxied = append(proxied, first(e.srv.URL, hdr, body))
	}
	d, p := p95(direct), p95(proxied)
	t.Logf("first byte p95: direct %v, through queqiaod %v (+%v)", d, p, p-d)
	if p-d > 5*time.Millisecond {
		t.Fatalf("proxy adds %v at p95", p-d)
	}
}

// queqiaod forwards from loopback, where magpie asks no key: listening
// beyond this computer would hand its subscriptions to the network
// (spec §5.5). Serve refuses before it opens the port.
func TestServeRefusesNonLoopbackListen(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.168.1.5:3426", "[::]:0"} {
		err := Serve(context.Background(), addr, http.NotFoundHandler())
		if err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Errorf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // returns as soon as it serves
		if err := Serve(ctx, addr, http.NotFoundHandler()); err != nil && strings.Contains(err.Error(), "loopback") {
			t.Errorf("%s refused: %v", addr, err)
		}
	}
}
