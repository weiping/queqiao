package wire_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/wire"
)

// fixture is one request as an agent sent it: the path, the headers that
// name its session, and the body's bytes. Real ones were captured from
// Claude Code 2.1 and Codex 0.162 against a scripted upstream (Task 2).
type fixture struct {
	name    string
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
	body    []byte
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	files, _ := filepath.Glob("testdata/*")
	var out []fixture
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fx := fixture{name: filepath.Base(f)}
		if strings.HasSuffix(f, ".raw") {
			fx.Path = "/v1/messages"
			fx.body = b
		} else {
			if err := json.Unmarshal(b, &fx); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			fx.body = fx.Body
		}
		out = append(out, fx)
	}
	if len(out) < 10 {
		t.Fatalf("only %d fixtures", len(out))
	}
	return out
}

func parseFixture(t *testing.T, name string) *wire.Request {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.name == name {
			p, _ := wire.ProtocolOf(f.Path)
			r, err := wire.Parse(p, f.body)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
	}
	t.Fatalf("no fixture %s", name)
	return nil
}

func TestProtocolOfPaths(t *testing.T) {
	cases := map[string]wire.Protocol{
		"/v1/messages": wire.Anthropic, "/messages": wire.Anthropic,
		"/v1/responses": wire.Responses, "/responses": wire.Responses,
		"/v1/chat/completions": wire.Chat, "/chat/completions": wire.Chat,
	}
	for path, want := range cases {
		if got, ok := wire.ProtocolOf(path); !ok || got != want {
			t.Errorf("%s: %v %v", path, got, ok)
		}
	}
	for _, path := range []string{"/v1/models", "/v1/messages/count_tokens", "/v1/embeddings"} {
		if _, ok := wire.ProtocolOf(path); ok {
			t.Errorf("%s has a protocol", path)
		}
	}
}

func TestUserTextStripsSystemReminders(t *testing.T) {
	r := parseFixture(t, "chat-turn2.json")
	if got := wire.UserText(r); got != "second question" {
		t.Fatalf("UserText = %q", got)
	}
}

func TestSessionOfOrderAndTruncation(t *testing.T) {
	h := http.Header{}
	h.Set("x-claude-code-session-id", "cc")
	h.Set(wire.SessionHeader, "mine")
	if got := wire.SessionOf(h); got != "mine" {
		t.Fatalf("got %q", got)
	}
	h = http.Header{}
	h.Set("x-claude-code-session-id", strings.Repeat("a", 200))
	if got := wire.SessionOf(h); len(got) != 128 {
		t.Fatalf("len %d", len(got))
	}
	h = http.Header{}
	h.Set("session-id", "codex")
	if got := wire.SessionOf(h); got != "codex" {
		t.Fatalf("codex session-id: %q", got)
	}
}

func TestToolsCountsErrorsPerProtocol(t *testing.T) {
	cases := map[string]wire.ToolStats{
		"anthropic-cc-tool-error.json":      {Calls: 1, Failures: 1},
		"anthropic-only-tool-result.json":   {Calls: 2, Failures: 1},
		"responses-codex-within.json":       {Calls: 1, Failures: 0}, // Responses outputs carry no error flag
		"responses-codex-turn2-within.json": {Calls: 2, Failures: 0},
		"chat-within.json":                  {Calls: 1, Failures: 0}, // nor do Chat tool messages
		"anthropic-cc-turn1.json":           {},
	}
	for name, want := range cases {
		if got := wire.Tools(parseFixture(t, name)); got != want {
			t.Errorf("%s: %+v want %+v", name, got, want)
		}
	}
}

func TestTurnOfAcrossARealCodexSession(t *testing.T) {
	cases := []struct {
		name   string
		turn   int
		within bool
	}{
		{"responses-codex-turn1.json", 1, false},
		{"responses-codex-within.json", 1, true},
		{"responses-codex-turn2.json", 2, false},
		{"responses-codex-turn2-within.json", 2, true},
	}
	var words string
	for i, c := range cases {
		r := parseFixture(t, c.name)
		turn, within := wire.TurnOf(r)
		if turn != c.turn || within != c.within {
			t.Errorf("%s: (%d,%v) want (%d,%v)", c.name, turn, within, c.turn, c.within)
		}
		if i == 0 {
			words = wire.FirstWords(r)
		} else if wire.FirstWords(r) != words {
			t.Errorf("%s: FirstWords changed within one session", c.name)
		}
	}
}

func TestParseRejectsTruncatedBody(t *testing.T) {
	if _, err := wire.Parse(wire.Anthropic, []byte(`{"messages":[{"role":"user","content":"hel`)); err == nil {
		t.Fatal("truncated body parsed")
	}
}
