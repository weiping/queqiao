package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
)

// The Gateway page lists the models a key can be held to, the routing
// groups first (Magic_zero on Discord), and keeps what was picked for it
// (#882).
func TestCallerKeyModelsRoutes(t *testing.T) {
	sandboxHome(t)
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"m", "m-mini"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "Mine", Members: []string{"relay/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	callerKeyRoutes(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	var l struct{ Models []keyModelJSON }
	if w := request("GET", "/api/caller-keys/models", ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &l) != nil {
		t.Fatal(w.Code, w.Body)
	}
	var ids []string
	for _, m := range l.Models {
		ids = append(ids, m.ID)
		if m.Group {
			if m.ID != "group/mine" || m.Name != "Mine" || m.Provider != "" {
				t.Fatalf("group %+v", m)
			}
			continue
		}
		if m.Provider != "relay" || m.ProviderName != "Relay" || m.Name == "" {
			t.Fatalf("%+v", m)
		}
	}
	if !slices.Equal(ids, []string{"group/mine", "relay/m", "relay/m-mini"}) {
		t.Fatal("models", ids)
	}
	var s struct{ Keys []access.Key }
	if w := request("POST", "/api/caller-keys/add-key", `{"name":"Phone"}`); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
		t.Fatal(w.Code, w.Body)
	}
	id := s.Keys[0].ID
	if w := request("POST", "/api/caller-keys/models-key", `{"key":"`+id+`","models":["relay/*","openai/gpt-5","group/mine"]}`); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
		t.Fatal(w.Code, w.Body)
	}
	if !slices.Equal(s.Keys[0].Models, []string{"relay/*", "openai/gpt-5", "group/mine"}) {
		t.Fatal("kept", s.Keys[0].Models)
	}
	if w := request("POST", "/api/caller-keys/models-key", `{"key":"`+id+`","models":["gpt-5"]}`); w.Code == 200 {
		t.Fatal("accepted a model without its provider")
	}
	var free struct{ Keys []access.Key }
	if w := request("POST", "/api/caller-keys/models-key", `{"key":"`+id+`","models":[]}`); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &free) != nil || free.Keys[0].Models != nil {
		t.Fatal("every model", w.Code, w.Body)
	}
}
