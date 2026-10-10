package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

const (
	testTurnID = "0d52a4c2-d0a6-453d-bf87-7a4eb1ac168a"
	testHeader = `{"session_id":"sess-1","turn_id":"` + testTurnID + `","model":"group/mbridge"}`
)

func headerWith(value string) http.Header {
	h := http.Header{}
	h.Set("x-codex-turn-metadata", value)
	return h
}

func TestCodexTurnIDFromHeader(t *testing.T) {
	got := CodexTurnID(headerWith(testHeader), nil)
	if got != testTurnID {
		t.Fatalf("got %q, want %q", got, testTurnID)
	}
}

func TestCodexTurnIDFromBodyClientMetadataObject(t *testing.T) {
	body := fmt.Sprintf(`{"model":"group/mbridge","client_metadata":{"session_id":"sess-1","x-codex-turn-metadata":%s}}`, testHeader)
	got := CodexTurnID(http.Header{}, []byte(body))
	if got != testTurnID {
		t.Fatalf("got %q, want %q", got, testTurnID)
	}
}

func TestCodexTurnIDFromBodyClientMetadataString(t *testing.T) {
	// Real spike shape, built from the inside out so each nesting level
	// carries exactly its own escaping: the body's client_metadata is a
	// JSON string whose x-codex-turn-metadata value is again a JSON string
	// holding the metadata object.
	inner, err := json.Marshal(map[string]string{
		"session_id": "sess-1",
		"turn_id":    testTurnID,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]string{
		"session_id":            "sess-1",
		"x-codex-turn-metadata": string(inner),
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"model":           "group/mbridge",
		"client_metadata": string(metadata),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := CodexTurnID(http.Header{}, body)
	if got != testTurnID {
		t.Fatalf("got %q, want %q", got, testTurnID)
	}
}

func TestCodexTurnIDHeaderGarbageFallsBackToBody(t *testing.T) {
	body := fmt.Sprintf(`{"client_metadata":{"x-codex-turn-metadata":{"turn_id":"%s"}}}`, testTurnID)
	got := CodexTurnID(headerWith("not json"), []byte(body))
	if got != testTurnID {
		t.Fatalf("got %q, want %q", got, testTurnID)
	}
}

func TestCodexTurnIDMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		body []byte
	}{
		{"both absent", http.Header{}, []byte(`{"model":"m"}`)},
		{"no body", http.Header{}, nil},
		{"header without turn_id", headerWith(`{"session_id":"s"}`), nil},
		{"body without client_metadata", http.Header{}, []byte(`{"model":"m","metadata":{}}`)},
		{"garbage body", http.Header{}, []byte(`not json`)},
		{"turn_id not a string", http.Header{}, []byte(`{"client_metadata":{"x-codex-turn-metadata":{"turn_id":42}}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodexTurnID(tc.h, tc.body); got != "" {
				t.Fatalf("got %q, want \"\"", got)
			}
		})
	}
}

func TestCodexTurnIDOversizedBody(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 1<<20+1)
	if got := CodexTurnID(http.Header{}, big); got != "" {
		t.Fatalf("got %q, want \"\"", got)
	}
}
