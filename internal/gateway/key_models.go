package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
)

// A gateway key may be held to some models (#882, access.Key.Models): it
// is shown only those in the model lists, and a request of it for another
// is refused before any provider is asked. A model is held to by its
// provider's id and its own, "<provider>/<model>", whatever the agent
// called it. A routing group passes when the key names it, "group/<id>"
// (or "group/*", every group), and then every member of it does, through
// it; else only when every model in it does. A group the key names inside
// one it doesn't counts for its own members.

// magpieChoseKey marks, in a request's context, a call magpie makes for a
// model the user picked in its settings rather than the caller (a web
// search's, an image description's, a Codex title's): the key's models
// don't hold it.
type magpieChoseKey struct{}

func magpieChose(ctx context.Context) context.Context {
	return context.WithValue(ctx, magpieChoseKey{}, true)
}

// keyHolds is the calling key's models, when they hold this request.
func keyHolds(r *http.Request) (access.Identity, bool) {
	who := access.Caller(r.Context())
	if !who.Restricted() {
		return who, false
	}
	if chose, _ := r.Context().Value(magpieChoseKey{}).(bool); chose {
		return who, false
	}
	return who, true
}

// modelAllowed says the key may use provider p's model.
func modelAllowed(who access.Identity, p provider.Provider, model string) bool {
	return who.Allows(p.ID + "/" + model)
}

// memberAllowed says the key may use a routing group's member: the model,
// or a group it is reached through that the key names.
func memberAllowed(who access.Identity, m provider.Member) bool {
	return who.Allows(append([]string{m.Provider.ID + "/" + m.Model}, m.Path...)...)
}

// groupNamed says the key names the routing group g itself.
func groupNamed(who access.Identity, g provider.Group) bool {
	return g.ID != "" && who.Allows(provider.GroupPrefix+g.ID)
}

// groupAllowed says the key may use the routing group g: it names it, or
// may use every member of it.
func groupAllowed(who access.Identity, g provider.Group, ms []provider.Member) bool {
	return groupNamed(who, g) || membersAllowed(who, ms)
}

// groupKeeps is the members of g a request through it may go to for the
// key: every one when it names g, else those it may use.
func groupKeeps(who access.Identity, g provider.Group, ms []provider.Member) map[string]bool {
	out := map[string]bool{}
	named := groupNamed(who, g)
	for _, m := range ms {
		if named || memberAllowed(who, m) {
			out[m.Provider.ID+"/"+m.Model] = true
		}
	}
	return out
}

// membersAllowed says the key may use every member of a routing group:
// one it may use only some of is the key's no more than an empty one.
func membersAllowed(who access.Identity, ms []provider.Member) bool {
	if len(ms) == 0 {
		return !who.Restricted()
	}
	for _, m := range ms {
		if !memberAllowed(who, m) {
			return false
		}
	}
	return true
}

// entryAllowed says the key is shown a model of the catalog.
func entryAllowed(who access.Identity, e provider.Entry) bool {
	if !who.Restricted() {
		return true
	}
	if e.Group != "" {
		g, ms, ok := provider.FindGroup(e.ID)
		return ok && groupAllowed(who, g, ms)
	}
	return modelAllowed(who, e.Provider, e.Model)
}

// keyAllowed filters the catalog to what the calling key may use.
func keyAllowed(r *http.Request, es []provider.Entry) []provider.Entry {
	who, held := keyHolds(r)
	if !held {
		return es
	}
	out := make([]provider.Entry, 0, len(es))
	for _, e := range es {
		if entryAllowed(who, e) {
			out = append(out, e)
		}
	}
	return out
}

// allowedCandidates leaves out of a plan the providers' models the key
// may not use: a model's fallbacks are other models. members are the
// group's the key may go to through it (groupKeeps), nil for a model.
func allowedCandidates(who access.Identity, cs []candidate, members map[string]bool) []candidate {
	out := cs[:0:0]
	for _, c := range cs {
		if members[c.p.ID+"/"+c.model] || modelAllowed(who, c.p, c.model) {
			out = append(out, c)
		}
	}
	return out
}

// keyModelError is what a request for a model its key may not use is told.
func keyModelError(who access.Identity, model string) string {
	return fmt.Sprintf("The gateway key %q may not use %s; it may use %s. Change the key's models in magpie's Gateway page, or use a model it has.",
		who.KeyName, model, strings.Join(who.Models, ", "))
}

// countHeld answers the 403 a gateway key held to some models (#882) gets
// for counting tokens on one it may not use, as it would be refused
// serving it: Anthropic's count_tokens and Gemini's :countTokens, id the
// model as each asks it, resolved — a bare group's name, an auto group's
// stand-in — and a group judged by its members. proto is the API the
// error is answered on.
func countHeld(w http.ResponseWriter, r *http.Request, proto provider.Protocol, id string) bool {
	keyWho, held := keyHolds(r)
	if !held {
		return false
	}
	if gid, ok := provider.GroupFor(id); ok {
		id = gid
	}
	if sid, ok := provider.AutoStandIn(id); ok {
		id = sid
	}
	p, model, ok := provider.Resolve(id)
	if !ok {
		return false // counted nowhere: a local estimate, as before
	}
	g, ms, isGroup := provider.FindGroup(id)
	if isGroup && !groupAllowed(keyWho, g, ms) || !isGroup && !modelAllowed(keyWho, p, model) {
		writeError(w, proto, http.StatusForbidden, keyModelError(keyWho, id))
		return true
	}
	return false
}
