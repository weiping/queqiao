package router

import (
	"sync"
	"time"

	"github.com/yetone/magpie/internal/gateway"
)

// sessionClock lets tests move time.
var sessionClock = time.Now

// sessionState is one session's router state.
type sessionState struct {
	tier         TurnState
	toolCalls    int
	toolFailures int
	lastAt       time.Time
	parent       string // set by MarkDerived / lineage
}

// Sessions keeps per-session router state in memory: the last committed
// TurnState, the gateway's own count of tool calls and failures, and when
// the session last made a request. Sessions idle for 24 hours are evicted.
type Sessions struct {
	mu sync.Mutex
	m  map[string]*sessionState
}

// NewSessions returns an empty store.
func NewSessions() *Sessions {
	return &Sessions{m: map[string]*sessionState{}}
}

// state returns the session's state, creating it if needed.
func (s *Sessions) state(key string) *sessionState {
	if s.m == nil {
		s.m = map[string]*sessionState{}
	}
	st, ok := s.m[key]
	if !ok {
		st = &sessionState{}
		s.m[key] = st
	}
	return st
}

// Get returns the session's committed turn state, or nil on the first turn.
func (s *Sessions) Get(key string) *TurnState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[key]
	if !ok || st.tier.Tier == "" {
		return nil
	}
	t := st.tier
	return &t
}

// Commit writes the session's turn state (§6.5: once per turn).
func (s *Sessions) Commit(key string, t TurnState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(key)
	st.tier = t
	st.lastAt = sessionClock()
	s.evictLocked(st.lastAt)
}

// Observe records what the gateway saw of a request: the session's tool
// call and failure counts (every message's tool_result parts; a Responses
// body's function_call_output never marks an error, S8) and the time.
func (s *Sessions) Observe(session string, req *gateway.Request) {
	calls, failures := 0, 0
	if req != nil {
		for _, m := range req.Messages {
			for _, p := range m.Parts {
				if p.Kind == gateway.ToolResult {
					calls++
					if p.IsError {
						failures++
					}
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := sessionClock()
	st := s.state(session)
	st.toolCalls, st.toolFailures, st.lastAt = calls, failures, now
	s.evictLocked(now)
}

// Stats returns the session's tool stats as of its last request.
func (s *Sessions) Stats(session string) (calls, failures int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[session]
	if !ok {
		return 0, 0
	}
	return st.toolCalls, st.toolFailures
}

// SinceLast reports how long ago the session's last request was (zero when
// the session is unknown).
func (s *Sessions) SinceLast(session string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[session]
	if !ok {
		return 0
	}
	return sessionClock().Sub(st.lastAt)
}

// MarkDerived records that session descends from parent (the /lineage
// path; the harness's parent_session reaches Decide directly).
func (s *Sessions) MarkDerived(session, parent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state(session).parent = parent
}

// ParentOf resolves the parent of a derived session: the explicitly marked
// parent if there is one, else the session a marked firstWords points at.
func (s *Sessions) ParentOf(session, firstWords string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.m[session]; ok && st.parent != "" {
		return st.parent, true
	}
	if firstWords == "" {
		return "", false
	}
	if p, ok := s.m[session+"|"+firstWords]; ok && p.parent != "" {
		return p.parent, true
	}
	return "", false
}

// InheritFrom copies the parent's turn state into the child as its Prev
// (§5.8: EscalatedLeft kept, LowerStreak reset); no parent state means
// nothing happens.
func (s *Sessions) InheritFrom(child, parent string) *TurnState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[parent]
	if !ok || p.tier.Tier == "" {
		return nil
	}
	t := p.tier
	t.LowerStreak = 0
	c := s.state(child)
	c.tier = t
	return &t
}

// evictLocked drops sessions idle past 24 hours. Caller holds the lock.
func (s *Sessions) evictLocked(now time.Time) {
	if len(s.m) < 1024 {
		return
	}
	for k, st := range s.m {
		if now.Sub(st.lastAt) > 24*time.Hour {
			delete(s.m, k)
		}
	}
}
