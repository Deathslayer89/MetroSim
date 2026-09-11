package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/Deathslayer89/MetroSim/internal/stats"
)

// writeCDFSVG draws the pickup-wait CDF, one line per policy. The x axis stops
// at the 99th percentile so a few very long waits don't squash the rest.
func writeCDFSVG(path string, summaries map[string]*policySummary) error {
	const (
		w, h           = 740, 460
		mL, mR, mT, mB = 60, 20, 30, 50
		plotW, plotH   = w - mL - mR, h - mT - mB
		labelColor     = "#333"
		gridColor      = "#ddd"
		lineWidth      = 2
	)

	names := make([]string, 0, len(summaries))
	for n := range summaries {
		names = append(names, n)
	}
	sort.Strings(names)

	var all []float64
	for _, n := range names {
		all = append(all, summaries[n].allWaits...)
	}
	xMax := niceCeil(stats.Percentile(all, 0.99))

	xPx := func(x float64) float64 { return float64(mL) + x/xMax*float64(plotW) }
	yPx := func(y float64) float64 { return float64(mT) + (1-y)*float64(plotH) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" font-family="sans-serif" font-size="12">`, w, h)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="white"/>`, w, h)

	for i := 0; i <= 10; i++ {
		x := xPx(xMax * float64(i) / 10)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="%s"/>`,
			x, mT, x, mT+plotH, gridColor)
	}
	for i := 0; i <= 10; i++ {
		y := yPx(float64(i) / 10)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s"/>`,
			mL, y, mL+plotW, y, gridColor)
	}

	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`,
		mL, mT+plotH, mL+plotW, mT+plotH, labelColor)
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`,
		mL, mT, mL, mT+plotH, labelColor)

	for i := 0; i <= 10; i++ {
		x := xPx(xMax * float64(i) / 10)
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" fill="%s" text-anchor="middle">%.0f</text>`,
			x, mT+plotH+18, labelColor, xMax*float64(i)/10)
	}
	for i := 0; i <= 10; i++ {
		y := yPx(float64(i) / 10)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" fill="%s" text-anchor="end">%.1f</text>`,
			mL-6, y+4, labelColor, float64(i)/10)
	}

	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" text-anchor="middle">Pickup wait (s), axis cut at the 99th percentile</text>`,
		mL+plotW/2, h-12, labelColor)
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" text-anchor="middle" transform="rotate(-90 %d %d)">Cumulative fraction of trips</text>`,
		18, mT+plotH/2, labelColor, 18, mT+plotH/2)

	colors := []string{"#2c7fb8", "#e34a33", "#31a354", "#756bb1"}
	for i, n := range names {
		waits := append([]float64(nil), summaries[n].allWaits...)
		sort.Float64s(waits)
		if len(waits) == 0 {
			continue
		}
		var pts strings.Builder
		fmt.Fprintf(&pts, "%.1f,%.1f", xPx(0), yPx(0))
		for k, x := range waits {
			if x > xMax {
				fmt.Fprintf(&pts, " %.1f,%.1f", xPx(xMax), yPx(float64(k)/float64(len(waits))))
				break
			}
			fmt.Fprintf(&pts, " %.1f,%.1f", xPx(x), yPx(float64(k+1)/float64(len(waits))))
		}
		color := colors[i%len(colors)]
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%d"/>`,
			pts.String(), color, lineWidth)

		legendY := mT + 18 + i*22
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="14" height="14" fill="%s"/>`,
			mL+plotW-160, legendY-12, color)
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s">%s (n=%d)</text>`,
			mL+plotW-140, legendY, labelColor, n, len(waits))
	}

	b.WriteString(`</svg>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// niceCeil rounds x up to 1, 2 or 5 times a power of ten.
func niceCeil(x float64) float64 {
	if x <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(x))
	base := math.Pow(10, exp)
	for _, m := range []float64{1, 2, 5, 10} {
		if m*base >= x {
			return m * base
		}
	}
	return 10 * base
}
