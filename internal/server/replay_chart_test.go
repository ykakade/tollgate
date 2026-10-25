package server

import (
	"fmt"
	"math"
	"strings"
)

// replaySVG draws the cost of each replay run as grouped bars: one group
// for each scenario, with the run with sticky routing over the run
// without. The SVG has its own light and dark colors.
func replaySVG(results []replayResult) []byte {
	const (
		width     = 720
		left      = 150 // the start of the bars
		right     = 130 // the room for the value labels
		top       = 104
		barH      = 18
		barGap    = 2
		groupGap  = 26
		axisSteps = 4
	)
	type group struct {
		name    string
		on, off replayResult
	}
	var groups []group
	for i := 0; i+1 < len(results); i += 2 {
		groups = append(groups, group{results[i].Scenario, results[i], results[i+1]})
	}
	maxCost := 0.0
	for _, r := range results {
		maxCost = math.Max(maxCost, usd(r.CostMicroUSD))
	}
	step := math.Ceil(maxCost*10/axisSteps) / 10
	scaleMax := step * axisSteps
	plotW := float64(width - left - right)
	x := func(v float64) float64 { return left + v/scaleMax*plotW }
	groupH := 2*barH + barGap
	plotH := len(groups)*groupH + (len(groups)-1)*groupGap
	height := top + plotH + 84

	last := groups[len(groups)-1]
	saving := 100 * (1 - usd(last.on.CostMicroUSD)/usd(last.off.CostMicroUSD))

	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...); b.WriteByte('\n') }
	p(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title desc" font-family="system-ui, -apple-system, 'Segoe UI', sans-serif">`, width, height, width, height)
	p(`<title id="title">Sticky routing cost %.0f%% less in the %s</title>`, saving, last.name)
	var desc []string
	for _, g := range groups {
		desc = append(desc, fmt.Sprintf("%s: %.4f USD with sticky routing, %.4f USD without", g.name, usd(g.on.CostMicroUSD), usd(g.off.CostMicroUSD)))
	}
	p(`<desc id="desc">Cost of one replayed agent session of 40 turns. %s.</desc>`, strings.Join(desc, "; "))
	p(`<style>
.surface{fill:#fcfcfb}.t1{fill:#0b0b0b}.t2{fill:#52514e}.t3{fill:#898781}
.grid{stroke:#e1e0d9;stroke-width:1}.base{stroke:#c3c2b7;stroke-width:1}
.on{fill:#2a78d6}.off{fill:#eb6834}
@media (prefers-color-scheme: dark){
.surface{fill:#1a1a19}.t1{fill:#f0efec}.t2{fill:#c3c2b7}
.grid{stroke:#2c2c2a}.base{stroke:#383835}
.on{fill:#3987e5}.off{fill:#d95926}}
text{font-size:12px}.title{font-size:16px;font-weight:600}.num{font-variant-numeric:tabular-nums}
</style>`)
	p(`<rect class="surface" width="%d" height="%d" rx="8"/>`, width, height)
	p(`<text class="t1 title" x="20" y="32">Sticky routing cost %.0f%% less in the %s</text>`, saving, last.name)
	p(`<text class="t2" x="20" y="52">Cost of one agent session of 40 requests, in USD. The primary target fails during the outage.</text>`)
	// Legend.
	p(`<rect class="on" x="20" y="68" width="12" height="12" rx="2"/><text class="t2" x="38" y="78">Sticky routing on</text>`)
	p(`<rect class="off" x="160" y="68" width="12" height="12" rx="2"/><text class="t2" x="178" y="78">Sticky routing off</text>`)
	// Grid and axis.
	for i := 0; i <= axisSteps; i++ {
		v := step * float64(i)
		gx := x(v)
		cls := "grid"
		if i == 0 {
			cls = "base"
		}
		p(`<line class="%s" x1="%.1f" y1="%d" x2="%.1f" y2="%d"/>`, cls, gx, top-8, gx, top+plotH+8)
		p(`<text class="t3 num" x="%.1f" y="%d" text-anchor="middle">%.2f</text>`, gx, top+plotH+24, v)
	}
	bar := func(cls string, y int, v float64) {
		w := x(v) - left
		r := math.Min(4, w)
		p(`<path class="%s" d="M%d %d H%.1f Q%.1f %d %.1f %.1f V%.1f Q%.1f %d %.1f %d H%d Z"/>`,
			cls, left, y, left+w-r, left+w, y, left+w, float64(y)+r, float64(y+barH)-r, left+w, y+barH, left+w-r, y+barH, left)
	}
	for i, g := range groups {
		y := top + i*(groupH+groupGap)
		p(`<text class="t1" x="20" y="%d">%s</text>`, y+barH+barGap/2+4, g.name)
		on, off := usd(g.on.CostMicroUSD), usd(g.off.CostMicroUSD)
		bar("on", y, on)
		bar("off", y+barH+barGap, off)
		onLabel := fmt.Sprintf("%.3f", on)
		if d := 100 * (1 - on/off); d >= 0.5 {
			onLabel += fmt.Sprintf(" (%.0f%% less)", d)
		}
		p(`<text class="t1 num" x="%.1f" y="%d">%s</text>`, x(on)+6, y+13, onLabel)
		p(`<text class="t2 num" x="%.1f" y="%d">%.3f</text>`, x(off)+6, y+barH+barGap+13, off)
	}
	p(`<text class="t3" x="20" y="%d">Synthetic session, two fake providers with a simulated 5-minute prompt cache, prices of claude-sonnet-5-5.</text>`, height-28)
	p(`<text class="t3" x="20" y="%d">Source: testdata/replay/results.json, made by TestStickyReplay (make replay).</text>`, height-12)
	p(`</svg>`)
	return []byte(b.String())
}

func usd(micro int64) float64 { return float64(micro) / 1e6 }
