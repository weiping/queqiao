package gateway

import (
	"context"
	"net/http"

	"github.com/yetone/magpie/internal/provider"
)

// RouterHit is what the queqiao router decided for a request (SP2): the
// tier it chose, why, and whether a stored hint answered instead.
type RouterHit struct {
	Tier   string `json:"tier"`
	Reason string `json:"reason,omitempty"`
	Source string `json:"source,omitempty"` // jev | llm | default | hint
	Hint   bool   `json:"hint,omitempty"`
	Arm    string `json:"arm,omitempty"` // router | control
}

// RouterHookFunc is the callback the queqiao router installs: it sees every
// group request whose group it manages (the router group and the tier
// groups), receives the raw body and the parsed IR request, and returns a
// RuleHit naming the member to put first — or nil to leave the order
// alone (the tier groups, which only get observed).
type RouterHookFunc func(h http.Header, body []byte, req *Request, g provider.Group, ms []provider.Member, agent string) *RuleHit

// routerHook is nil until the router wires itself in at startup (main);
// with it nil, groups behave exactly as before. routerManages says which
// groups the callback wants: only those are parsed for it and passed to
// it, so a user's own groups pay nothing for the router being installed.
var (
	routerHook    RouterHookFunc
	routerManages func(g provider.Group) bool
)

// SetRouterHook installs (or with nil, removes) the router callback and
// the predicate naming the groups it manages.
func SetRouterHook(f RouterHookFunc, manages func(g provider.Group) bool) {
	routerHook, routerManages = f, manages
}

// MuxRegister holds extra endpoint registrations the gateway's mux runs
// after its own (the router's /v1/queqiao/* endpoints); main appends to it
// so the gateway never imports the router package.
var MuxRegister []func(*http.ServeMux)

// SessionOf, FirstWords, TurnOf and UserText export the pieces of request
// identity the router hook needs; they are thin wrappers so the router
// package does not reach into unexported gateway internals.

func SessionOf(h http.Header) string { return sessionOf(h) }

func FirstWords(req *Request) string { return firstWords(req) }

func TurnOf(req *Request) (turn int, within bool) { return turnIn(req) }

func UserText(req *Request) string { return userText(req) }

// AskDecider posts a System One request body for asked (e.g.
// "typesafe/jev-latest") to its decider provider, its own usage recorded
// as magpie's decide calls are; for the router's Jev classifier.
func (s *Server) AskDecider(ctx context.Context, asked, body string) ([]byte, error) {
	p, model, err := provider.RouteDecider(asked)
	if err != nil {
		return nil, err
	}
	return s.systemOne(ctx, p, model, []byte(body))
}

// AskChat posts a chat-completions body through the gateway itself, as
// its own classifiers ask a model; for the router's plain-model
// classifier.
func (s *Server) AskChat(ctx context.Context, model, body string) (string, error) {
	return s.askChat(model, []byte(body))
}
