package router

import (
	"sync"
	"time"
)

// hintClock lets tests move time.
var hintClock = time.Now

// HintKey identifies a hint (§6.5): the Codex user-prompt hook stored it at
// /turn time; the gateway hook takes it when the model request arrives.
// Which of the three fields are set depends on the side: Put carries what
// /turn knew (session, turn id, prompt hash), Take carries what the request
// offered.
type HintKey struct {
	Session    string
	TurnID     string
	PromptHash string
}

// Hint is a stored /turn decision, waiting for its request.
type Hint struct {
	Key  HintKey
	Tier Tier
	At   time.Time
}

// Hints stores /turn hints for the gateway hook. Entries live 120 seconds
// and are consumed on take.
type Hints struct {
	mu    sync.Mutex
	hints []Hint
}

// NewHints returns an empty store.
func NewHints() *Hints { return &Hints{} }

// Put stores a hint (replacing one with the same key fields).
func (h *Hints) Put(hint Hint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hint.At.IsZero() {
		hint.At = hintClock()
	}
	out := h.hints[:0]
	for _, old := range h.hints {
		if old.Key != hint.Key {
			out = append(out, old)
		}
	}
	h.hints = append(out, hint)
}

// live reports whether the hint is within its TTL.
func live(hint Hint, now time.Time) bool { return now.Sub(hint.At) <= 120*time.Second }

// takeWhere removes and returns the first live hint f matches.
func (h *Hints) takeWhere(now time.Time, f func(HintKey) bool) (Hint, bool) {
	for i, hint := range h.hints {
		if live(hint, now) && f(hint.Key) {
			h.hints = append(h.hints[:i], h.hints[i+1:]...)
			return hint, true
		}
	}
	return Hint{}, false
}

// Take returns and removes the hint for k, trying §6.5's order: (a) session
// and turn id, (b) session and prompt hash, (c) prompt hash alone.
func (h *Hints) Take(k HintKey) (Hint, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := hintClock()
	if k.Session != "" && k.TurnID != "" {
		if hint, ok := h.takeWhere(now, func(key HintKey) bool {
			return key.Session == k.Session && key.TurnID == k.TurnID
		}); ok {
			return hint, true
		}
	}
	if k.Session != "" && k.PromptHash != "" {
		if hint, ok := h.takeWhere(now, func(key HintKey) bool {
			return key.Session == k.Session && key.PromptHash == k.PromptHash
		}); ok {
			return hint, true
		}
	}
	if k.PromptHash != "" {
		if hint, ok := h.takeWhere(now, func(key HintKey) bool {
			return key.PromptHash == k.PromptHash
		}); ok {
			return hint, true
		}
	}
	return Hint{}, false
}
