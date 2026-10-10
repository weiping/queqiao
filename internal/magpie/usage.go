package magpie

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"
)

// UsageRow is one request in magpie's ledger, the columns queqiao's
// reports read (from `magpie usage --csv`).
type UsageRow struct {
	Time           time.Time
	Session        string
	RequestedModel string // what the agent asked for: group/qq-<tier> for a routed turn
	Provider       string
	Model          string
	Input          int
	Output         int
	CacheRead      int
	CacheWrite     int
	CostUSD        float64
	Priced         bool // magpie knew the model's price (cost_usd was not empty)
	Status         int
}

// usageColumns are the columns ParseUsageCSV needs.
var usageColumns = []string{"time", "session", "requested_model", "provider", "model",
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cost_usd", "status"}

// Usage is magpie's ledger for a period: today, 7d, 30d or all.
func (c *Client) Usage(ctx context.Context, period string) ([]UsageRow, error) {
	out, err := c.cli(ctx, "usage", "--csv", period)
	if err != nil {
		return nil, err
	}
	return ParseUsageCSV(bytes.NewReader(out))
}

// ParseUsageCSV reads magpie's usage CSV by its column names: their order
// and columns it does not know do not matter; a missing one is an error
// naming it.
func ParseUsageCSV(r io.Reader) ([]UsageRow, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("magpie usage CSV: %w", err)
	}
	at := map[string]int{}
	for i, h := range head {
		at[h] = i
	}
	for _, c := range usageColumns {
		if _, ok := at[c]; !ok {
			return nil, fmt.Errorf("magpie usage CSV has no %q column", c)
		}
	}
	var rows []UsageRow
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("magpie usage CSV line %d: %w", line, err)
		}
		get := func(c string) string {
			if i := at[c]; i < len(rec) {
				return rec[i]
			}
			return ""
		}
		num := func(c string) int { n, _ := strconv.Atoi(get(c)); return n }
		row := UsageRow{Session: get("session"), RequestedModel: get("requested_model"), Provider: get("provider"),
			Model: get("model"), Input: num("input_tokens"), Output: num("output_tokens"),
			CacheRead: num("cache_read_tokens"), CacheWrite: num("cache_write_tokens"), Status: num("status")}
		if row.Time, err = time.Parse(time.RFC3339, get("time")); err != nil {
			return nil, fmt.Errorf("magpie usage CSV line %d: time %q", line, get("time"))
		}
		if s := get("cost_usd"); s != "" {
			if row.CostUSD, err = strconv.ParseFloat(s, 64); err != nil {
				return nil, fmt.Errorf("magpie usage CSV line %d: cost_usd %q", line, s)
			}
			row.Priced = true
		}
		rows = append(rows, row)
	}
}
