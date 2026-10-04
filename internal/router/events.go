package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
)

// Event is one line of router.jsonl: a decision, a hint consumed, a
// feedback signal, or a shadow-tier log entry.
type Event struct {
	Kind       string  `json:"kind"` // decide | hint_consumed | feedback | shadow
	Time       string  `json:"t"`    // RFC3339
	Session    string  `json:"session,omitempty"`
	Harness    string  `json:"harness,omitempty"`
	Agent      string  `json:"agent,omitempty"`
	Tier       Tier    `json:"tier,omitempty"`
	ShadowTier Tier    `json:"shadow_tier,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Source     string  `json:"source,omitempty"`
	Arm        string  `json:"arm,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	LatencyMs  int     `json:"latency_ms,omitempty"`
	Extra      string  `json:"extra,omitempty"` // feedback kind/value, free-form
}

var (
	eventsMu    sync.Mutex
	eventsPath  string // set by SetEventsPath; empty = appdir default
	eventsClock = func() string { return time.Now().UTC().Format(time.RFC3339) }
)

// defaultEventsPath is ~/.config/queqiao/router.jsonl.
func defaultEventsPath() string {
	return filepath.Join(appdir.Config(), "router.jsonl")
}

// SetEventsPath points the event log somewhere else (tests).
func SetEventsPath(p string) {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	eventsPath = p
}

// Append writes one event as a JSON line to the event log
// (~/.config/queqiao/router.jsonl, overridable).
func Append(ev Event) error {
	if ev.Time == "" {
		ev.Time = eventsClock()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	p := eventsPath
	if p == "" {
		p = defaultEventsPath()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
