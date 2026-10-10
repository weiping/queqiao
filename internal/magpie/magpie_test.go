package magpie

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func readFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/usage.csv")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUsageColumnsMatch(t *testing.T) {
	rows, err := ParseUsageCSV(strings.NewReader(readFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[2] // group/qq-fast (recorded before the rename), session s1, served by up/m1
	if r.Session != "s1" || r.RequestedModel != "group/qq-fast" /* recorded before the rename */ || r.Provider != "up" || r.Model != "m1" ||
		r.Input != 5 || r.Output != 1 || r.Status != 200 || r.Priced || r.Time.IsZero() {
		t.Fatalf("row %+v", r)
	}
}

func TestUsageCSVByNameNotPosition(t *testing.T) {
	recs, _ := csv.NewReader(strings.NewReader(readFixture(t))).ReadAll()
	// reverse the columns and add one magpie may add some day
	var b strings.Builder
	w := csv.NewWriter(&b)
	for i, rec := range recs {
		out := []string{"zzz"}
		if i > 0 {
			out[0] = "x"
		}
		for j := len(rec) - 1; j >= 0; j-- {
			out = append(out, rec[j])
		}
		w.Write(out)
	}
	w.Flush()
	want, _ := ParseUsageCSV(strings.NewReader(readFixture(t)))
	got, err := ParseUsageCSV(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestUsageCSVMissingColumnNamesIt(t *testing.T) {
	recs, _ := csv.NewReader(strings.NewReader(readFixture(t))).ReadAll()
	col := -1
	for j, h := range recs[0] {
		if h == "cost_usd" {
			col = j
		}
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	for _, rec := range recs {
		w.Write(append(append([]string{}, rec[:col]...), rec[col+1:]...))
	}
	w.Flush()
	_, err := ParseUsageCSV(strings.NewReader(b.String()))
	if err == nil || !strings.Contains(err.Error(), "cost_usd") {
		t.Fatalf("err = %v", err)
	}
}

func TestUsagePricedRow(t *testing.T) {
	in := "time,session,requested_model,provider,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd,status\n" +
		"2026-10-10T02:43:31Z,s,group/mb-perf,p,m,10,2,3,4,0.0125,200\n"
	rows, err := ParseUsageCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if !r.Priced || r.CostUSD != 0.0125 || r.CacheRead != 3 || r.CacheWrite != 4 {
		t.Fatalf("%+v", r)
	}
}

type fakeMagpie struct {
	*httptest.Server
	path, ua, auth string
	body           string
}

func newFake(t *testing.T, status int, reply string) *fakeMagpie {
	f := &fakeMagpie{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.path, f.ua, f.auth, f.body = r.URL.Path, r.UserAgent(), r.Header.Get("Authorization"), string(b)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(f.Close)
	return f
}

func TestSystemOnePostsBodyVerbatim(t *testing.T) {
	f := newFake(t, 200, `{"answers":[]}`)
	c := New(f.URL)
	body := `{"model":"typesafe/jev-latest", "questions":[1]}`
	out, err := c.SystemOne(context.Background(), []byte(body))
	if err != nil || string(out) != `{"answers":[]}` {
		t.Fatalf("%s %v", out, err)
	}
	if f.path != "/v1/systemone" || f.body != body {
		t.Fatalf("path %s body %s", f.path, f.body)
	}
}

func TestChatSendsMbridgeUserAgent(t *testing.T) {
	f := newFake(t, 200, `{"choices":[{"message":{"content":"2"}}]}`)
	got, err := New(f.URL).Chat(context.Background(), "deepseek/deepseek-chat", []byte(`{"model":"deepseek/deepseek-chat"}`))
	if err != nil || got != "2" {
		t.Fatalf("%q %v", got, err)
	}
	if f.path != "/v1/chat/completions" || f.ua != UserAgent || UserAgent != "mbridge/1" {
		t.Fatalf("path %s ua %s", f.path, f.ua)
	}
}

func TestErrorCarriesMagpieMessage(t *testing.T) {
	f := newFake(t, 404, `{"error":{"message":"magpie knows no model"}}`)
	_, err := New(f.URL).Chat(context.Background(), "x/y", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "magpie knows no model") {
		t.Fatalf("err = %v", err)
	}
}

func TestKeySentAsBearerWhenSet(t *testing.T) {
	f := newFake(t, 200, `{"data":[]}`)
	c := New(f.URL)
	c.Models(context.Background())
	if f.auth != "" {
		t.Fatalf("auth sent without a key: %q", f.auth)
	}
	c.Key = "sk-gw"
	c.Models(context.Background())
	if f.auth != "Bearer sk-gw" {
		t.Fatalf("auth %q", f.auth)
	}
}

func TestModelsListsIDs(t *testing.T) {
	f := newFake(t, 200, `{"data":[{"id":"fake/m1"},{"id":"group/mb-fast"}]}`)
	ids, err := New(f.URL).Models(context.Background())
	if err != nil || strings.Join(ids, ",") != "fake/m1,group/mb-fast" {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestHealthFailsWhenNothingListens(t *testing.T) {
	f := newFake(t, 200, `{}`)
	url := f.URL
	f.Close()
	if err := New(url).Health(context.Background()); err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(url, "http://")) {
		t.Fatalf("err = %v", err)
	}
}

func TestGroupAddArgs(t *testing.T) {
	var got []string
	c := New("http://127.0.0.1:1")
	c.Bin = "magpie"
	c.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return nil, nil
	}
	if err := c.GroupAdd(context.Background(), "mb-fast", []string{"a/x", "b/y"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "magpie group add mb-fast models=a/x,b/y routing=order" {
		t.Fatalf("args %q", got)
	}
	c.SetAgentModel(context.Background(), "pi", "group/mb-balanced")
	if strings.Join(got, " ") != "magpie pi group/mb-balanced" {
		t.Fatalf("args %q", got)
	}
	c.Usage(context.Background(), "30d")
	if strings.Join(got, " ") != "magpie usage --csv 30d" {
		t.Fatalf("args %q", got)
	}
}

func TestGroupsParsesIDs(t *testing.T) {
	c := New("http://127.0.0.1:1")
	c.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("  models    1 fake/m1  fake · m1\n  mb-fast  group/mb-fast  order  fake/m1\n  mbridge  group/mbridge  order  group/mb-balanced\n"), nil
	}
	ids, err := c.Groups(context.Background())
	if err != nil || strings.Join(ids, ",") != "mb-fast,mbridge" {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestCLIErrorCarriesOutput(t *testing.T) {
	c := New("http://127.0.0.1:1")
	c.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("magpie: no such provider"), io.ErrUnexpectedEOF
	}
	err := c.GroupAdd(context.Background(), "mb-fast", []string{"x/y"})
	if err == nil || !strings.Contains(err.Error(), "no such provider") || !strings.Contains(err.Error(), "group add") {
		t.Fatalf("err = %v", err)
	}
}

func TestModelListReadsContextWindow(t *testing.T) {
	f := newFake(t, 200, `{"data":[{"id":"group/mb-fast","context_window":200000},{"id":"x/y"}]}`)
	ms, err := New(f.URL).ModelList(context.Background())
	if err != nil || len(ms) != 2 || ms[0] != (Model{ID: "group/mb-fast", Context: 200000}) || ms[1].Context != 0 {
		t.Fatalf("%+v %v", ms, err)
	}
}
