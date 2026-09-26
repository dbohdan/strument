// Command larkspur answers the README's question: over twenty years,
// how much does the committee's rule beat lazier strategies?
//
// By default it runs every strategy on a fresh 12-bed garden for 20
// seasons (one per year) across 200 seeds and prints a table of the
// totals per strategy — mean, 10th and 90th percentile, and the mean
// as a percentage of the committee rotation's — then one line per
// strategy with a Unicode sparkline of that strategy's mean harvest
// per season, all lines on one shared scale so the shapes can be
// compared. Plain text, no color.
//
// With -sweep it instead reruns the comparison with the persistent
// families' fade rate — the model's least certain number — at each
// of several rates across its plausible range, printing every
// strategy's percentage of rotation per rate and which strategy
// comes out first.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"larkspur/garden"
	"larkspur/sim"
)

// row is one strategy's results across every seed.
type row struct {
	name      string
	mean      float64 // mean total harvest
	p10, p90  int     // percentiles of the total harvest
	pct       float64 // mean as a percentage of rotation's
	perSeason []int   // mean harvest per season across seeds
}

// strategies are the ones under comparison, rotation first: the
// percentage column and the sweep's first place both use it as the
// reference.
var strategies = []struct {
	name string
	s    sim.Strategy
}{
	{"rotation", sim.Rotation{}}, // the committee's rule
	{"monoculture", sim.Monoculture{}},
	{"alternation", sim.Alternation{}},
	{"greedy", sim.Greedy{}},
}

func main() {
	seasons := flag.Int("seasons", 20, "seasons per run, one per year")
	seeds := flag.Int("seeds", 200, "seeds (runs) per strategy")
	beds := flag.Int("beds", sim.DefaultBeds, "beds per garden")
	seed := flag.Int64("seed", 0, "first seed; the rest follow in order")
	sweep := flag.Bool("sweep", false, "sweep the persistent-family fade rate instead of printing the table")
	flag.Parse()

	if *seasons < 1 {
		fatalf("-seasons must be at least 1")
	}
	if *seeds < 1 {
		fatalf("-seeds must be at least 1")
	}
	if *beds < 1 {
		fatalf("-beds must be at least 1")
	}

	if *sweep {
		sweepFade(*seasons, *seeds, *beds, *seed)
		return
	}

	rows := measure(*seasons, *seeds, *beds, *seed, nil)
	printTable(rows)
	printSparklines(rows)
}

// measure runs every strategy over every seed with the given fade
// rates (nil for the package defaults) and works out each row's
// percentage of rotation.
func measure(seasons, seeds, beds int, first int64, fades garden.Fades) []row {
	rows := make([]row, len(strategies))
	for i, st := range strategies {
		rows[i] = run(st.name, st.s, seasons, seeds, beds, first, fades)
	}
	// rows[0] is the committee rotation, the reference for the
	// percentage column.
	for i := range rows {
		if rows[0].mean > 0 {
			rows[i].pct = 100 * rows[i].mean / rows[0].mean
		}
	}
	return rows
}

// sweepFade reruns the comparison with the persistent families'
// fade rate — brassicas and alliums — at each rate in turn, from
// nearly permanent to a whole point per season, and reports each
// strategy's percentage of rotation plus which strategy wins.
func sweepFade(seasons, seeds, beds int, first int64) {
	// Rates in tenths of pressure per season away: 0.1 takes ten
	// seasons to shed a point, 1.0 is the rate the fast families
	// already get.
	rates := []int{1, 2, 3, 5, 7, 10}

	const rateW, colW = 11, 12
	fmt.Printf("%*s", rateW, "fade/season")
	for _, st := range strategies {
		fmt.Printf(" %*s", colW, st.name)
	}
	fmt.Printf(" %*s\n", colW, "first")

	for _, rate := range rates {
		fades := garden.DefaultFades()
		fades[garden.Brassicas], fades[garden.Alliums] = rate, rate

		rows := measure(seasons, seeds, beds, first, fades)

		best := 0
		for i := range rows {
			if rows[i].mean > rows[best].mean {
				best = i
			}
		}

		fmt.Printf("%*.1f", rateW, float64(rate)/10)
		for _, r := range rows {
			fmt.Printf(" %*s", colW, fmt.Sprintf("%.0f%%", r.pct))
		}
		fmt.Printf(" %*s\n", colW, rows[best].name)
	}
}

// run plays one strategy over every seed and summarizes the totals.
// Fades of nil mean the package defaults.
func run(name string, strategy sim.Strategy, seasons, seeds, beds int, first int64, fades garden.Fades) row {
	totals := make([]int, seeds)
	perSeason := make([]int, seasons)
	for i := 0; i < seeds; i++ {
		g := sim.NewGarden(beds)
		if fades != nil {
			g = g.WithFades(fades)
		}
		res := sim.Run(g, strategy, seasons, first+int64(i))
		totals[i] = res.Total
		for s, harvest := range res.PerSeason {
			perSeason[s] += harvest
		}
	}
	sort.Ints(totals)

	var sum int
	for _, total := range totals {
		sum += total
	}
	for s := range perSeason {
		perSeason[s] /= seeds
	}
	return row{
		name:      name,
		mean:      float64(sum) / float64(seeds),
		p10:       percentile(totals, 0.10),
		p90:       percentile(totals, 0.90),
		perSeason: perSeason,
	}
}

// percentile returns the p quantile (0..1) of sorted values by
// nearest rank.
func percentile(sorted []int, p float64) int {
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		return sorted[0]
	}
	if rank > len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[rank-1]
}

func printTable(rows []row) {
	const nameW, numW, pctW = 12, 8, 14
	fmt.Printf("%-*s %*s %*s %*s %*s\n",
		nameW, "strategy", numW, "mean", numW, "p10", numW, "p90", pctW, "% of rotation")
	for _, r := range rows {
		fmt.Printf("%-*s %*.0f %*d %*d %*s\n",
			nameW, r.name, numW, r.mean, numW, r.p10, numW, r.p90,
			pctW, fmt.Sprintf("%.0f%%", r.pct))
	}
}

func printSparklines(rows []row) {
	scale := 0
	for _, r := range rows {
		for _, v := range r.perSeason {
			if v > scale {
				scale = v
			}
		}
	}
	fmt.Println()
	fmt.Println("mean harvest per season")
	for _, r := range rows {
		fmt.Printf("%-12s %s\n", r.name, sparkline(r.perSeason, scale))
	}
}

// sparkline maps values to Unicode bars. Every line is drawn on the
// same scale so their levels compare, not just their shapes.
func sparkline(values []int, scale int) string {
	levels := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range values {
		i := 0
		if scale > 0 {
			i = (v*7 + scale/2) / scale
		}
		if i >= len(levels) {
			i = len(levels) - 1
		}
		b.WriteRune(levels[i])
	}
	return b.String()
}

func fatalf(msg string) {
	fmt.Fprintf(os.Stderr, "larkspur: %s\n", msg)
	os.Exit(1)
}
