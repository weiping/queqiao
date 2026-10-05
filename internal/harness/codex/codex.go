package codex

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/yetone/magpie/internal/harness"
)

/**
 * The Codex hook handlers (§6.9): UserPromptSubmit picks the turn's tier
 * (the gateway's prompt hint routes the request that follows), PreToolUse
 * picks a spawn's model, PostToolUse reports PR links. Inputs are the JSON
 * shapes Codex 0.160 sends (S7–S13); every handler fails silent: no /turn,
 * no output, exit 0 — the gateway's own routing takes over.
 */

const routingGroup = "group/queqiao"

// prLink matches a GitHub pull-request URL anywhere in the raw input, so
// field-name drift between Codex versions cannot hide a link (§6.9).
var prLink = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)

// forkedFrom matches the thread-fork metadata pointer wherever Codex
// nests it (§5.8; S13 saw it under spawn metadata, the exact spot in
// UserPromptSubmit input is defensive).
var forkedFrom = regexp.MustCompile(`"forked_from_thread_id"\s*:\s*"([^"]+)"`)

type input struct {
	HookEventName string         `json:"hook_event_name"`
	SessionID     string         `json:"session_id"`
	TurnID        string         `json:"turn_id"`
	Cwd           string         `json:"cwd"`
	Prompt        string         `json:"prompt"`
	Model         string         `json:"model"`
	ToolName      string         `json:"tool_name"`
	ToolInput     map[string]any `json:"tool_input"`
}

// UserPrompt handles UserPromptSubmit: /turn for routed prompts, a
// manual-switch event for pinned ones. Output: none, ever.
func UserPrompt(ctx context.Context, stdin []byte, c *harness.Client) ([]byte, error) {
	var in input
	if err := json.Unmarshal(stdin, &in); err != nil {
		return nil, nil
	}
	if in.SessionID == "" || in.Prompt == "" {
		return nil, nil
	}
	if in.Model != "" && in.Model != routingGroup {
		// the user pinned a model (or Codex switched): report, don't route.
		// dedup happens in the report, not here (§6.9 note).
		c.Feedback(ctx, map[string]any{
			"session": in.SessionID,
			"kind":    "manual_model_switch",
			"value":   in.Model,
		})
		return nil, nil
	}
	body := map[string]any{
		"harness":    "codex",
		"session":    in.SessionID,
		"prompt":     in.Prompt,
		"agent":      "main",
		"plan_mode":  false, // S8: no plan-mode marker outside the TUI
		"store_hint": true,  // the gateway matches the request that follows
	}
	if in.TurnID != "" {
		body["turn_id"] = in.TurnID
	}
	if in.Cwd != "" {
		body["cwd"] = in.Cwd
	}
	if m := forkedFrom.FindSubmatch(stdin); m != nil {
		body["parent_session"] = string(m[1])
	}
	_, err := c.Turn(ctx, body)
	if err != nil {
		return nil, nil // this turn goes out as group/queqiao: gateway mode
	}
	return nil, nil
}

// PreAgent handles PreToolUse on spawn_agent: pins a model on spawns that
// carry none. Output: the updatedInput decision, or nothing.
func PreAgent(ctx context.Context, stdin []byte, c *harness.Client) ([]byte, error) {
	var in input
	if err := json.Unmarshal(stdin, &in); err != nil {
		return nil, nil
	}
	if in.ToolInput == nil {
		return nil, nil
	}
	if s, _ := in.ToolInput["model"].(string); s != "" {
		return nil, nil // already chosen
	}
	agentType, _ := in.ToolInput["agent_type"].(string)
	if agentType == "" {
		agentType = "default"
	}
	// the gateway's R1 (fixed agents) sees the type and answers without a
	// classification for known ones — one table, kept on the server
	message, _ := in.ToolInput["message"].(string)
	out, err := c.Turn(ctx, map[string]any{
		"harness":    "codex",
		"session":    in.SessionID,
		"prompt":     message,
		"agent":      agentType,
		"store_hint": false,
	})
	if err != nil {
		return nil, nil // spawn inherits its parent's model
	}
	group, _ := out["group"].(string)
	if group == "" {
		return nil, nil
	}
	updated := make(map[string]any, len(in.ToolInput)+1)
	for k, v := range in.ToolInput {
		updated[k] = v
	}
	updated["model"] = group
	decision, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "allow",
			"updatedInput":       updated,
		},
	})
	if err != nil {
		return nil, nil
	}
	return decision, nil
}

// PostBash handles PostToolUse on Bash: reports a PR link if the raw
// input holds one. Output: none.
func PostBash(ctx context.Context, stdin []byte, c *harness.Client) ([]byte, error) {
	m := prLink.Find(stdin)
	if m == nil {
		return nil, nil
	}
	session := ""
	var in input
	if err := json.Unmarshal(stdin, &in); err == nil {
		session = in.SessionID
	}
	c.Feedback(ctx, map[string]any{
		"session": session,
		"kind":    "pr_created",
		"value":   string(m),
	})
	return nil, nil
}
