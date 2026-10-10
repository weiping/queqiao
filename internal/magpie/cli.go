package magpie

import (
	"context"
	"strings"
)

func (c *Client) cli(ctx context.Context, args ...string) ([]byte, error) {
	out, err := c.Run(ctx, c.Bin, args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return out, &Error{Op: strings.Join(args, " "), Msg: msg}
	}
	return out, nil
}

// GroupAdd makes (or replaces) the routing group id with members in
// order: `magpie group add <id> models=… routing=order`.
func (c *Client) GroupAdd(ctx context.Context, id string, members []string) error {
	_, err := c.cli(ctx, "group", "add", id, "models="+strings.Join(members, ","), "routing=order")
	return err
}

// GroupSet changes a group: `magpie group set <id> k=v…`.
func (c *Client) GroupSet(ctx context.Context, id string, kv ...string) error {
	_, err := c.cli(ctx, append([]string{"group", "set", id}, kv...)...)
	return err
}

// Groups lists the ids of magpie's routing groups (`magpie groups`).
func (c *Client) Groups(ctx context.Context) ([]string, error) {
	out, err := c.cli(ctx, "groups")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.HasPrefix(f[1], GroupPrefix) {
			ids = append(ids, strings.TrimPrefix(f[1], GroupPrefix))
		}
	}
	return ids, nil
}

// Version is what `magpie version` prints.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.cli(ctx, "version")
	return strings.TrimSpace(string(out)), err
}

// SetAgentModel points an agent at a model through magpie's own wiring:
// `magpie <agent> <model>`.
func (c *Client) SetAgentModel(ctx context.Context, agent, model string) error {
	_, err := c.cli(ctx, agent, model)
	return err
}
