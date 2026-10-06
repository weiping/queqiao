package gui

import (
	"cmp"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/provider"
)

// keyRow is a gateway key as the Gateway page lists it, with what it has
// used of its limit (#585).
type keyRow struct {
	access.Key
	Used *budget.Status `json:"used,omitempty"`
}

// keyModelJSON is a model a gateway key may be held to, as its picker
// lists it.
type keyModelJSON struct {
	ID           string `json:"id"` // "<provider>/<model>" or "group/<id>", what the key keeps
	Name         string `json:"name"`
	Provider     string `json:"provider,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	Group        bool   `json:"group,omitempty"` // a routing group (Magic_zero on Discord)
}

func withLimits(keys []access.Key) []keyRow {
	now := time.Now()
	out := make([]keyRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyRow{Key: k, Used: budget.Of(k, now)})
	}
	return out
}

func callerKeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/caller-keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		access.MigrateLegacyLANKeyBestEffort()
		keys, err := access.List()
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]any{"keys": withLimits(keys)})
	})
	// the models a key may be held to (#882): the routing groups, then
	// each provider's; a group the key names is its with every member
	mux.HandleFunc("GET /api/caller-keys/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		out := []keyModelJSON{}
		for _, e := range provider.Catalog() {
			if e.Group != "" {
				out = append(out, keyModelJSON{ID: e.ID, Name: cmp.Or(e.Name, e.Group), Group: true})
				continue
			}
			out = append(out, keyModelJSON{ID: e.Provider.ID + "/" + e.Model, Name: cmp.Or(e.Name, e.Model), Provider: e.Provider.ID, ProviderName: cmp.Or(e.Provider.Name, e.Provider.ID)})
		}
		writeJSON(w, map[string]any{"models": out})
	})
	mux.HandleFunc("POST /api/caller-keys/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in access.Change
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, err)
			return
		}
		access.MigrateLegacyLANKeyBestEffort()
		secret, err := access.Update(r.PathValue("action"), in)
		if err != nil {
			fail(w, err)
			return
		}
		keys, err := access.List()
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, map[string]any{"keys": withLimits(keys), "secret": secret})
	})
}
