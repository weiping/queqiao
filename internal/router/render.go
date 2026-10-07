package router

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

/**
 * The §9 report as text (and the same shape as JSON). Each metric gets
 * one line with both arms side by side; the sample-short warning sits
 * under the merge rates, per §9.
 */

// Render writes the text report.
func Render(w io.Writer, rep Report) {
	row := func(name string, format func(a ArmReport) string) {
		fmt.Fprintf(w, "%-28s %-16s %s\n", name, format(rep.Router), format(rep.Control))
	}
	fmt.Fprintln(w, strings.Repeat("─", 64))
	fmt.Fprintf(w, "%-28s %-16s %s\n", "queqiao router report", "router", "control")
	fmt.Fprintln(w, strings.Repeat("─", 64))

	row("sessions", func(a ArmReport) string { return fmt.Sprintf("%d", a.Sessions) })

	row("cost/session median", func(a ArmReport) string {
		return fmt.Sprintf("$%.4f [%.4f,%.4f]", a.CostMedian, a.CostCILo, a.CostCIHi)
	})
	row("cost/session mean", func(a ArmReport) string { return fmt.Sprintf("$%.4f", a.CostMean) })
	row("cost/session p90", func(a ArmReport) string { return fmt.Sprintf("$%.4f", a.CostP90) })

	row("tier distribution", func(a ArmReport) string { return dist(a.TierDistribution) })
	if len(rep.Control.ShadowDistribution) > 0 {
		row("  (control: router's pick)", func(a ArmReport) string { return dist(a.ShadowDistribution) })
	}

	if rowHasEscalation(rep) {
		row("升档率", func(a ArmReport) string { return pctMap(a.EscalateRates) })
		row("未升档轮次的选低率", func(a ArmReport) string { return fmt.Sprintf("%.1f%%", a.UnderRateKept*100) })
	}

	row("PR sessions", func(a ArmReport) string {
		return fmt.Sprintf("%d (%.0f%%)", a.PRSessions, pct(a.PRSessions, a.Sessions))
	})
	row("merged-PR sessions", func(a ArmReport) string {
		return fmt.Sprintf("%d (%.0f%%)", a.MergedSessions, pct(a.MergedSessions, a.Sessions))
	})

	row("manual model switch", func(a ArmReport) string {
		return fmt.Sprintf("%d (%.0f%%)", a.ManualSwitchSessions, pct(a.ManualSwitchSessions, a.Sessions))
	})

	row("hint hit rate", func(a ArmReport) string {
		d := a.HintConsumed + a.GatewayDecides
		if d == 0 {
			return "—"
		}
		return fmt.Sprintf("%.0f%% (%d/%d)", 100*float64(a.HintConsumed)/float64(d), a.HintConsumed, d)
	})

	row("cache-write cost share", func(a ArmReport) string {
		if a.TotalCost == 0 {
			return "—"
		}
		return fmt.Sprintf("%.0f%%", 100*a.CacheWriteCost/a.TotalCost)
	})

	fmt.Fprintln(w, strings.Repeat("─", 64))
	fmt.Fprintf(w, "merged-rate z (router − control): %.2f\n", rep.MergedZ)
	if rep.SampleShort {
		fmt.Fprintln(w, "样本不足（每组 <100 会话）：合并率仅供参考，结论只看成本与手动换模型率")
	}
	var notes []string
	if n := rep.Router.UnpricedRequests + rep.Control.UnpricedRequests; n > 0 {
		notes = append(notes, fmt.Sprintf("%d 个请求无价目，未计入成本", n))
	}
	if len(notes) > 0 {
		fmt.Fprintln(w, strings.Join(notes, "；"))
	}
	fmt.Fprintln(w, "口径：成本=会话内全部 2xx 请求价目和；分组=decide/shadow 最后带 arm 的事件；提示命中率只含经路由组的请求")
}

// RenderJSON writes the report as JSON (the same fields, machine-read).
func RenderJSON(w io.Writer, rep Report) {
	type armOut struct {
		Arm                  string             `json:"arm"`
		Sessions             int                `json:"sessions"`
		CostMedian           float64            `json:"cost_median"`
		CostCILo             float64            `json:"cost_ci_lo"`
		CostCIHi             float64            `json:"cost_ci_hi"`
		CostMean             float64            `json:"cost_mean"`
		CostP90              float64            `json:"cost_p90"`
		Tiers                map[string]float64 `json:"tiers,omitempty"`
		ShadowTiers          map[string]float64 `json:"shadow_tiers,omitempty"`
		PRSessions           int                `json:"pr_sessions"`
		MergedSessions       int                `json:"merged_sessions"`
		ManualSwitchSessions int                `json:"manual_switch_sessions"`
		HintConsumed         int                `json:"hint_consumed"`
		GatewayDecides       int                `json:"gateway_decides"`
		CacheWriteCost       float64            `json:"cache_write_cost"`
		TotalCost            float64            `json:"total_cost"`
		UnpricedRequests     int                `json:"unpriced_requests"`
	}
	conv := func(a ArmReport) armOut {
		return armOut{a.Arm, a.Sessions, a.CostMedian, a.CostCILo, a.CostCIHi, a.CostMean, a.CostP90,
			a.TierDistribution, a.ShadowDistribution, a.PRSessions, a.MergedSessions,
			a.ManualSwitchSessions, a.HintConsumed, a.GatewayDecides,
			a.CacheWriteCost, a.TotalCost, a.UnpricedRequests}
	}
	out := struct {
		Router      armOut  `json:"router"`
		Control     armOut  `json:"control"`
		MergedZ     float64 `json:"merged_z"`
		SampleShort bool    `json:"sample_short"`
	}{conv(rep.Router), conv(rep.Control), rep.MergedZ, rep.SampleShort}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	io.WriteString(w, string(b)+"\n")
}

func pct(x, n int) float64 {
	if n == 0 {
		return 0
	}
	return 100 * float64(x) / float64(n)
}

func dist(m map[string]float64) string {
	if len(m) == 0 {
		return "—"
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %.0f%%", k, 100*m[k])
	}
	return strings.Join(parts, " ")
}

func jsonMap(m map[string]float64) string {
	if len(m) == 0 {
		return "null"
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%q: %v", k, m[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

/**
 * The §5.3 calibration tables. The fixed first line says what the labels
 * can and cannot see: they only show dissatisfaction the user expressed.
 */

// RenderCalibrate writes the calibration tables.
func RenderCalibrate(w io.Writer, cals []ScoreCalibration) {
	fmt.Fprintln(w, "标签只反映用户表达出来的不满；要更准的判断，用 --csv 导出逐轮明细人工核对。")
	for _, c := range cals {
		dir := "越低越该升档"
		if c.HigherEscalates {
			dir = "越高越该升档"
		}
		fmt.Fprintf(w, "\n%s（%s）当前阈值 %.2f", scoreName(c.Score), dir, c.Current)
		switch {
		case c.Suggested != nil:
			fmt.Fprintf(w, "，建议阈值 %.2f", *c.Suggested)
		case c.SampleShort:
			fmt.Fprint(w, "，样本不足")
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%-14s %8s %8s %10s\n", "分段", "样本", "占比", "选低率")
		for _, b := range c.Bands {
			fmt.Fprintf(w, "%-14s %8d %7.1f%% %9.1f%%\n",
				fmt.Sprintf("%.2f–%.2f", b.Lo, b.Hi), b.N, b.Share*100, b.UnderRate*100)
		}
		fmt.Fprintf(w, "升档率 %.1f%%，未升档轮次的选低率 %.1f%%", c.EscalateRate*100, c.UnderRateKept*100)
		if c.Suggested != nil {
			fmt.Fprintf(w, "；阈值改为 %.2f 后：升档率 %.1f%%，未升档轮次的选低率 %.1f%%",
				*c.Suggested, c.ProjEscalateRate*100, c.ProjUnderRateKept*100)
		}
		fmt.Fprintln(w)
	}
}

// rowHasEscalation reports whether either arm has SP7 monitoring data, so
// an old ledger's report keeps its original shape.
func rowHasEscalation(rep Report) bool {
	return len(rep.Router.EscalateRates) > 0 || len(rep.Control.EscalateRates) > 0
}

// pctMap prints an arm's escalation rates, in a stable order.
func pctMap(m map[string]float64) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", k, m[k]*100))
	}
	return strings.Join(parts, " ")
}

func scoreName(s string) string {
	switch s {
	case "tier":
		return "档位置信度"
	case "dissatisfied":
		return "不满分数"
	case "review":
		return "复核分数"
	}
	return s
}

// CalibrateCSV writes the per-turn detail §5.3 asks for: the three scores
// and the label, never the user's words or the answer.
func CalibrateCSV(w io.Writer, events []Event, cfg Config, f CalibrateFilter) {
	fmt.Fprintln(w, "session,turn_id,harness,agent,tier,tier_confidence,dissatisfied,unresolved,under_tiered")
	for _, t := range calTurns(events, cfg, f) {
		fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%s,%v\n",
			escapeCSV(t.session), escapeCSV(t.turnID), escapeCSV(t.harness), escapeCSV(t.agent),
			t.tier, optScore(t.tierConf), optScore(t.dissatisfied), optScore(t.unresolved),
			t.label("tier"))
	}
}

func optScore(f *float64) string {
	if f == nil {
		return ""
	}
	return formatScore(*f)
}
