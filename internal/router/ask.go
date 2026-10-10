package router

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yetone/magpie/internal/magpie"
)

// AskVia is the classifier's ask callback over official magpie (SP8): a
// TypeSafe decider (typesafe/…) is asked at magpie's System One endpoint,
// any other model through its Chat Completions, both with magpie's keys.
func AskVia(c *magpie.Client) func(ctx context.Context, model, body string) (string, error) {
	return func(ctx context.Context, model, body string) (string, error) {
		if !strings.HasPrefix(model, "typesafe/") {
			return c.Chat(ctx, model, []byte(body))
		}
		// System One finds the decider by the body's model prefix; the
		// classifier writes the decider's own name there (jev-latest)
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			return "", err
		}
		m["model"], _ = json.Marshal(model)
		b, err := json.Marshal(m)
		if err != nil {
			return "", err
		}
		out, err := c.SystemOne(ctx, b)
		return string(out), err
	}
}
