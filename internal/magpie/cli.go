package magpie

import (
	"context"
	"errors"
	"fmt"
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

// ErrNoGroup is magpie having no routing group of that id.
var ErrNoGroup = errors.New("magpie has no such group")

// Group is one routing group as `magpie group <id>` shows it.
type Group struct {
	ID      string
	Members []GroupMember
}

// GroupMember is one model of a group. NotServed is magpie's reason when it
// skips the model (its provider is off, say); empty when it serves it.
type GroupMember struct {
	Model     string
	NotServed string
}

// Served is whether magpie serves at least one of the group's models. A
// group with none is left out of /v1/models, and asking for it fails.
func (g *Group) Served() bool {
	for _, m := range g.Members {
		if m.NotServed == "" {
			return true
		}
	}
	return false
}

// Group reads `magpie group <id>`: its members and why magpie skips any.
// The models line reads "models    1 <provider/model>  <note>", each
// further member on its own line as "<n> <provider/model>  <note>", where
// the note is "not served: <reason>" for a model magpie skips.
func (c *Client) Group(ctx context.Context, id string) (*Group, error) {
	out, err := c.cli(ctx, "group", id)
	if err != nil {
		if strings.Contains(string(out), "no group") {
			return nil, fmt.Errorf("%w: %s", ErrNoGroup, id)
		}
		return nil, err
	}
	g := &Group{ID: id}
	inModels := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "models" {
			inModels, f = true, f[1:]
		} else if !inModels {
			continue
		}
		if len(f) < 2 || !isNumber(f[0]) {
			if inModels && len(g.Members) > 0 {
				break
			}
			continue
		}
		m := GroupMember{Model: f[1]}
		if _, why, ok := strings.Cut(line, "not served:"); ok {
			m.NotServed = strings.TrimSpace(why)
		}
		g.Members = append(g.Members, m)
	}
	if !inModels {
		return nil, &Error{Op: "group " + id, Msg: "no models line in: " + strings.TrimSpace(string(out))}
	}
	return g, nil
}

func isNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
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
