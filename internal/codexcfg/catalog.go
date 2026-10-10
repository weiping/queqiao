package codexcfg

import (
	_ "embed"
	"encoding/json"
)

// prompt is Codex's generic system prompt (Apache-2.0, openai/codex,
// core/gpt-5.2-codex_prompt.md), as magpie hands third-party models.
//
//go:embed codex_prompt.md
var prompt string

// entry is one model of Codex's model_catalog_json. Its fields are those
// magpie writes for a model of its own (internal/codexcat, upstream
// 0d5fdbb2), checked against the file official magpie wrote for Codex
// 0.162 (testdata/catalog-from-magpie.json).
type entry struct {
	Slug          string     `json:"slug"`
	DisplayName   string     `json:"display_name"`
	Description   string     `json:"description"`
	Instructions  string     `json:"base_instructions"`
	DefaultEffort *string    `json:"default_reasoning_level"`
	Efforts       []struct{} `json:"supported_reasoning_levels"`
	Shell         string     `json:"shell_type"`
	Visibility    string     `json:"visibility"`
	InAPI         bool       `json:"supported_in_api"`
	Priority      int        `json:"priority"`
	Verbosity     bool       `json:"support_verbosity"`
	DefVerbosity  *string    `json:"default_verbosity"`
	ApplyPatch    string     `json:"apply_patch_tool_type"`
	Truncation    struct {
		Mode  string `json:"mode"`
		Limit int    `json:"limit"`
	} `json:"truncation_policy"`
	Tools      []string `json:"experimental_supported_tools"`
	Modalities []string `json:"input_modalities"`
	Tiers      []string `json:"service_tiers"`
	SearchTool bool     `json:"supports_search_tool"`
	Parallel   bool     `json:"supports_parallel_tool_calls"`
	Summaries  bool     `json:"supports_reasoning_summaries"`
}

// Catalog is a model_catalog_json listing ids, in order.
func Catalog(ids []string) []byte {
	out := struct {
		Models []entry `json:"models"`
	}{Models: []entry{}}
	for i, id := range ids {
		e := entry{Slug: id, DisplayName: id, Description: id + " via mbridge", Instructions: prompt,
			Efforts: []struct{}{}, Shell: "unified_exec", Visibility: "list", InAPI: true, Priority: i + 1,
			ApplyPatch: "freeform", Tools: []string{}, Modalities: []string{"text"}, Tiers: []string{},
			SearchTool: true, Parallel: true}
		e.Truncation.Mode, e.Truncation.Limit = "tokens", 10000
		out.Models = append(out.Models, e)
	}
	b, _ := json.MarshalIndent(out, "", " ")
	return b
}
