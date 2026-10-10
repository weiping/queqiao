package codexcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func home(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if config != "" {
		b, err := os.ReadFile(filepath.Join("testdata", config))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "config.toml"), b, 0o600)
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCodexInitKeepsUserTablesAndIsIdempotent(t *testing.T) {
	dir := home(t, "user-config.toml")
	before := read(t, filepath.Join(dir, "config.toml"))
	changed, err := Init(dir, "http://127.0.0.1:3426/v1")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "config.toml")); got != before {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if len(changed) != 2 {
		t.Fatalf("first init changed %v", changed)
	}
	profile := read(t, filepath.Join(dir, "queqiao.config.toml"))
	for _, want := range []string{
		`model_provider = "queqiao"`,
		`model = "group/queqiao"`,
		`model_catalog_json = ` + quote(filepath.Join(dir, "queqiao-models.json")),
		"[model_providers.queqiao]",
		`base_url = "http://127.0.0.1:3426/v1"`,
		`wire_api = "responses"`,
		`http_headers = { "x-openai-actor-authorization" = "magpie" }`,
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile lacks %s:\n%s", want, profile)
		}
	}
	changed, err = Init(dir, "http://127.0.0.1:3426/v1")
	if err != nil || len(changed) != 0 {
		t.Fatalf("second init changed %v (%v)", changed, err)
	}
}

func TestCodexInitCopiesBearerOnlyWhenPresent(t *testing.T) {
	dir := home(t, "user-config.toml")
	Init(dir, "http://127.0.0.1:3426/v1")
	if p := read(t, filepath.Join(dir, "queqiao.config.toml")); !strings.Contains(p, `experimental_bearer_token = "sk-gw-123"`) {
		t.Fatalf("no bearer:\n%s", p)
	}
	dir = home(t, "")
	if _, err := Init(dir, "http://127.0.0.1:3426/v1"); err != nil {
		t.Fatal(err)
	}
	if p := read(t, filepath.Join(dir, "queqiao.config.toml")); strings.Contains(p, "experimental_bearer_token") {
		t.Fatalf("bearer without a magpie table:\n%s", p)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("init created config.toml")
	}
}

func TestCatalogShapeMatchesMagpies(t *testing.T) {
	var magpie struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	json.Unmarshal([]byte(read(t, "testdata/catalog-from-magpie.json")), &magpie)
	var ours struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(Catalog([]string{"group/queqiao", "group/qq-fast"}), &ours); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	if len(ours.Models) != 2 || keys(ours.Models[0]) != keys(magpie.Models[0]) {
		t.Fatalf("ours %s\nmagpie %s", keys(ours.Models[0]), keys(magpie.Models[0]))
	}
	var slug string
	json.Unmarshal(ours.Models[1]["slug"], &slug)
	if slug != "group/qq-fast" {
		t.Fatalf("slug %q", slug)
	}
}

func TestReadTablesSplitsTopAndTables(t *testing.T) {
	top, tables := ReadTables([]byte("a = 1\n\n[x]\nb = 2\n[x.y]\nc = [\n  1,\n]\n"))
	if strings.Join(top, "|") != "a = 1|" || len(tables) != 2 || tables[1].Name != "x.y" || len(tables[1].Lines) != 4 {
		t.Fatalf("top %q tables %+v", top, tables)
	}
}
