package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/weiping/queqiao/internal/magpie"
)

// fakeMagpie answers /v1/systemone and /v1/chat/completions, recording
// what it was sent.
type fakeMagpie struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	jev    string // System One reply
	chat   func(body string) string
}

func (f *fakeMagpie) start(t *testing.T) *magpie.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths, f.bodies = append(f.paths, r.URL.Path), append(f.bodies, string(b))
		f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/systemone":
			io.WriteString(w, f.jev)
		case "/v1/chat/completions":
			out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": f.chat(string(b))}}}})
			w.Write(out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return magpie.New(srv.URL)
}

func TestAskViaRoutesJevToSystemOne(t *testing.T) {
	f := &fakeMagpie{jev: `{"answers":{"tier":{"choice":"performance","confidence":0.9},"dissatisfied":{"noul":0.1}}}`}
	c := f.start(t)
	v, err := NewClassifier(testConfig("typesafe/jev-latest"), AskVia(c)).Classify(context.Background(),
		Question{Message: "redesign the storage layer", Criteria: testCriteria, PreviousTier: TierBalanced})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier != TierPerformance || v.TierConfidence != 0.9 {
		t.Fatalf("verdict %+v", v)
	}
	if len(f.paths) != 1 || f.paths[0] != "/v1/systemone" {
		t.Fatalf("paths %v", f.paths)
	}
	// magpie's System One finds the Jev provider by the model's prefix: the
	// body must name typesafe/jev-latest, not the bare jev-latest the
	// decider itself is asked for
	var body struct {
		Model string `json:"model"`
	}
	json.Unmarshal([]byte(f.bodies[0]), &body)
	if body.Model != "typesafe/jev-latest" {
		t.Fatalf("model in body %q", body.Model)
	}
}

func TestAskViaRoutesPlainModelsToChat(t *testing.T) {
	f := &fakeMagpie{chat: func(body string) string {
		if strings.Contains(body, `"response_format"`) {
			return `{"tier":"fast","confidence":0.7,"dissatisfied":0.1}`
		}
		return "1"
	}}
	c := f.start(t)
	v, err := NewClassifier(testConfig("deepseek/deepseek-chat"), AskVia(c)).Classify(context.Background(),
		Question{Message: "what does this flag do", Criteria: testCriteria, PreviousTier: TierFast})
	if err != nil {
		t.Fatal(err)
	}
	if v.Tier != TierFast || v.TierConfidence != 0.7 {
		t.Fatalf("verdict %+v", v)
	}
	for _, p := range f.paths {
		if p != "/v1/chat/completions" {
			t.Fatalf("paths %v", f.paths)
		}
	}
}

// The same Jev answer gives the same verdict through magpie's HTTP as it
// did through the fork's in-process call (TestJevClassify's reply).
func TestAskViaClassifierParityWithFork(t *testing.T) {
	reply := `{"answers":{"tier":{"choice":"balanced","confidence":0.83},"dissatisfied":{"noul":0.2}}}`
	q := Question{Message: "fix the flaky test", PreviousTier: TierBalanced, Agent: "main", Criteria: testCriteria}
	direct, err := NewClassifier(testConfig("typesafe/jev-latest"), func(context.Context, string, string) (string, error) { return reply, nil }).
		Classify(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeMagpie{jev: reply}
	viaHTTP, err := NewClassifier(testConfig("typesafe/jev-latest"), AskVia(f.start(t))).Classify(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if *direct != *viaHTTP {
		t.Fatalf("direct %+v, via magpie %+v", direct, viaHTTP)
	}
}
