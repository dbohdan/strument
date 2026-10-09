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
//
// With -perennial it sweeps how long one asparagus planting stands
// before it moves — 4 to 20 seasons, at three fusarium build rates
// — printing the garden's mean total harvest per stand length,
// against the rotation-only garden as a reference.
//
// With -breakeven it prints, for the same stands and rates, the
// multiplier a unit of asparagus harvest needs for the garden with
// one asparagus bed to match the garden with none — a ratio of
// totals the runs already produce, with no price table anywhere.
//
// With -paired it runs every strategy on the same seeds — each seed
// is one weather sequence all strategies share — and reports how
// often rotation beats each strategy and rotation's worst margin
// against it.
//
// With -order it ranks all 120 distinct cyclic orders of the six
// families (brassicas first) on shared seeds and reports where the
// committee's order lands.
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

// beds in the sweeps: rotation alone, then the perennial garden at
// each fusarium build rate, in tenths of pressure per season.
var gardens = []struct {
	name  string
	build int
}{
	{"rotation", 0},
	{"build 0.2", 2},
	{"build 0.3", 3},
	{"build 0.4", 4},
}

// freshGarden returns a garden for the sweep row with the given
// build rate: 0 means plain rotation beds with the package
// defaults; otherwise the asparagus beds build at that rate.
func freshGarden(beds, build int) func() sim.Garden {
	return func() sim.Garden {
		if build == 0 {
			return sim.NewGarden(beds)
		}
		builds := garden.DefaultBuilds()
		builds[garden.Asparagus] = build
		return sim.NewGarden(beds).WithBuilds(builds)
	}
}

func main() {
	seasons := flag.Int("seasons", 20, "seasons per run, one per year")
	seeds := flag.Int("seeds", 200, "seeds (runs) per strategy")
	beds := flag.Int("beds", sim.DefaultBeds, "beds per garden")
	seed := flag.Int64("seed", 0, "first seed; the rest follow in order")
	sweep := flag.Bool("sweep", false, "sweep the persistent-family fade rate instead of printing the table")
	perennial := flag.Bool("perennial", false, "sweep how long an asparagus planting stands (4-20 seasons) instead of printing the table")
	breakeven := flag.Bool("breakeven", false, "print the asparagus value multiplier per stand length instead of printing the table")
	paired := flag.Bool("paired", false, "compare strategies seed by seed on the same weather instead of printing the table")
	order := flag.Bool("order", false, "rank all 120 cyclic family orders on shared seeds instead of printing the table")
	potassium := flag.Bool("potassium", false, "sweep the yearly potassium dressing and report what rotation's beds need instead of printing the table")
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
	if *perennial {
		perennialStand(*seasons, *seeds, *beds, *seed)
		return
	}
	if *breakeven {
		breakEven(*seasons, *seeds, *beds, *seed)
		return
	}
	if *paired {
		pairedCompare(*seasons, *seeds, *beds, *seed)
		return
	}
	if *order {
		orderRank(*seasons, *seeds, *beds, *seed)
		return
	}
	if *potassium {
		potassiumNeed(*seasons, *seeds, *beds, *seed)
		return
	}

	rows := measure(*seasons, *seeds, *beds, *seed, nil)
	printTable(rows)
	printSparklines(rows)
}

// pairedCompare runs every strategy on the same seeds — each seed
// is one weather sequence all strategies share — and reports, per
// strategy, how often rotation's total beats it and rotation's
// worst margin over all seeds.
func pairedCompare(seasons, seeds, beds int, first int64) {
	type stats struct {
		wins, ties, losses int
		worst, worstAt     int64
	}
	all := make([]stats, len(strategies)-1)

	for i := 0; i < seeds; i++ {
		s := first + int64(i)
		totals := make([]int64, len(strategies))
		for j, st := range strategies {
			res := sim.Run(sim.NewGarden(beds), st.s, seasons, s)
			totals[j] = int64(res.Total)
		}

		for j := range all {
			// strategies[0] is rotation, the reference.
			margin := totals[0] - totals[j+1]
			switch {
			case margin > 0:
				all[j].wins++
			case margin < 0:
				all[j].losses++
			default:
				all[j].ties++
			}
			if i == 0 || margin < all[j].worst {
				all[j].worst, all[j].worstAt = margin, s
			}
		}
	}

	fmt.Println("every strategy on the same seeds: each seed is one weather")
	fmt.Printf("sequence, so each row compares %d like-for-like runs against rotation\n\n", seeds)

	const nameW, colW = 12, 6
	fmt.Printf("%-*s %*s %*s %*s  %s\n",
		nameW, "strategy", colW, "wins", colW, "ties", colW, "losses", "worst margin")
	for j, st := range strategies[1:] {
		s := all[j]
		fmt.Printf("%-*s %*d %*d %*d  %+d (seed %d)\n",
			nameW, st.name, colW, s.wins, colW, s.ties, colW, s.losses, s.worst, s.worstAt)
	}
}

// potassiumCost runs rotation on the shared seeds with the given
// potassium dressing and reports the mean total, the mean number of
// bed-seasons in which a bed had no potassium left after its crop,
// and the number of seeds on which any bed ran short at all.
func potassiumCost(k, seasons, seeds, beds int, first int64) (mean float64, shortMean float64, shortSeeds int) {
	dressing := garden.Compost
	dressing.Potassium = k
	var total, short int
	for i := 0; i < seeds; i++ {
		g := sim.NewGarden(beds).WithDressing(dressing)
		res := sim.Run(g, sim.Rotation{}, seasons, first+int64(i))
		total += res.Total
		seedShort := 0
		for bed := 0; bed < beds; bed++ {
			for _, r := range res.History[bed] {
				if r.Potassium == 0 {
					seedShort++
				}
			}
		}
		short += seedShort
		if seedShort > 0 {
			shortSeeds++
		}
	}
	return float64(total) / float64(seeds), float64(short) / float64(seeds), shortSeeds
}

// cycle plants one fixed cyclic order of families: bed and season
// together shift each bed's place in the cycle, the same way
// Rotation walks FamilyOrder.
type cycle struct{ order []garden.Family }

func (c cycle) Choose(g sim.Garden, bed, season int) sim.Planting {
	return sim.Use(c.order[(bed+season)%len(c.order)])
}

func orderString(order []garden.Family) string {
	names := make([]string, len(order))
	for i, f := range order {
		names[i] = f.String()
	}
	return strings.Join(names, "-")
}

// cyclicOrders returns every distinct cyclic order of the six
// families: 5! = 120, with rotationally equal orders folded together
// by fixing brassicas first.
func cyclicOrders() [][]garden.Family {
	var rest []garden.Family
	for _, f := range garden.FamilyOrder {
		if f != garden.Brassicas {
			rest = append(rest, f)
		}
	}
	var out [][]garden.Family
	var permute func(prefix []garden.Family, left []garden.Family)
	permute = func(prefix, left []garden.Family) {
		if len(left) == 0 {
			out = append(out, append([]garden.Family{garden.Brassicas}, prefix...))
			return
		}
		for i := range left {
			rest2 := append(append([]garden.Family(nil), left[:i]...), left[i+1:]...)
			permute(append(append([]garden.Family(nil), prefix...), left[i]), rest2)
		}
	}
	permute(nil, rest)
	return out
}

// potassiumNeed asks how much potassium the committee's yearly
// compost would have to carry for rotation's beds never to run short,
// and what the shortfall costs at today's dressing. It sweeps the
// potassium dressing from today's value (1) upward and reports, per
// dressing, the mean total, the cost against today's compost, the
// mean count of short bed-seasons, and how many seeds saw any short.
func potassiumNeed(seasons, seeds, beds int, first int64) {
	base, _, _ := potassiumCost(garden.Compost.Potassium, seasons, seeds, beds, first)

	fmt.Printf("rotation, %d seeds, %d beds, %d seasons: potassium per bed in the\n", seeds, beds, seasons)
	fmt.Printf("yearly dressing against the committee's compost of %d.\n\n", garden.Compost.Potassium)
	fmt.Printf("%-10s %10s %10s %14s %12s\n", "dressing", "mean", "cost", "short bed-yr", "short seeds")

	need := -1
	for k := garden.Compost.Potassium; k <= 6; k++ {
		mean, short, shortSeeds := potassiumCost(k, seasons, seeds, beds, first)
		fmt.Printf("%-10d %10.0f %10.0f %14.1f %12d\n",
			k, mean, base-mean, short, shortSeeds)
		if need < 0 && shortSeeds == 0 {
			need = k
		}
	}
	fmt.Println()
	if need < 0 {
		fmt.Println("no dressing up to 6 keeps every bed above zero on every seed")
	} else {
		fmt.Printf("smallest dressing with no short bed-seasons on any seed: %d\n", need)
	}
}

// orderRank runs every cyclic family order — brassicas first, the
// other five permuted, 120 in all — on the same seeds, ranks them
// by mean total, and reports where the committee's order lands and
// what the best and worst orders look like. Wins counts the seeds
// on which an order beat the committee, so a difference between two
// orders is not weather.
func orderRank(seasons, seeds, beds int, first int64) {
	committee := cycle{order: garden.FamilyOrder[:]}
	committeeTotals := make([]int, seeds)
	for i := 0; i < seeds; i++ {
		committeeTotals[i] = sim.Run(sim.NewGarden(beds), committee, seasons, first+int64(i)).Total
	}

	type scored struct {
		order string
		mean  float64
		wins  int
	}
	orders := cyclicOrders()
	scores := make([]scored, len(orders))
	for o, order := range orders {
		var sum int
		wins := 0
		for i := 0; i < seeds; i++ {
			total := sim.Run(sim.NewGarden(beds), cycle{order: order}, seasons, first+int64(i)).Total
			sum += total
			if total > committeeTotals[i] {
				wins++
			}
		}
		scores[o] = scored{order: orderString(order), mean: float64(sum) / float64(seeds), wins: wins}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].mean > scores[j].mean })

	name := orderString(garden.FamilyOrder[:])
	rank := 0
	for i, s := range scores {
		if s.order == name {
			rank = i
			break
		}
	}
	fmt.Printf("every cyclic order (brassicas first) ranked by mean total over %d shared seeds\n", seeds)
	fmt.Printf("committee: %s\n", name)
	fmt.Printf("rank %d of %d, mean %.0f\n\n", rank+1, len(scores), scores[rank].mean)

	show := func(r int) {
		s := scores[r]
		fmt.Printf("%4d %5.0f %5d  %s\n", r+1, s.mean, s.wins, s.order)
	}
	fmt.Println("rank mean wins  order")
	for r := 0; r < 5; r++ {
		show(r)
	}
	fmt.Println("...")
	for r := len(scores) - 5; r < len(scores); r++ {
		show(r)
	}
}

// perennialStand sweeps how many seasons one asparagus planting
// stands before it moves, from 4 to 20, at three fusarium build
// rates (0.3, the package's, in the middle), and prints the garden's
// mean total for each. The rotation-only garden, with no perennial
// at all, is build-independent and comes first as the reference.
func perennialStand(seasons, seeds, beds int, first int64) {
	fmt.Println("mean garden total by stand length; fusarium build rate in points")
	fmt.Printf("per season (0.3 is the committed rate). Rotation with no asparagus: %.0f\n\n",
		meanTotal(freshGarden(beds, 0), sim.Rotation{}, seasons, seeds, first))

	fmt.Printf("%-6s", "K")
	for _, gr := range gardens[1:] {
		fmt.Printf(" %11s", gr.name)
	}
	fmt.Println()
	for k := 4; k <= 20; k++ {
		fmt.Printf("%-6d", k)
		for _, gr := range gardens[1:] {
			mean := meanTotal(freshGarden(beds, gr.build), sim.Perennial{Seasons: k}, seasons, seeds, first)
			fmt.Printf(" %11.0f", mean)
		}
		fmt.Println()
	}
}

// breakEven sweeps the stand length K and, per fusarium build rate,
// prints the multiplier a unit of asparagus harvest needs for the
// garden with one asparagus bed to match the rotation-only garden.
// It also reports the K where each column's multiplier is lowest.
func breakEven(seasons, seeds, beds int, first int64) {
	none := meanTotal(freshGarden(beds, 0), sim.Rotation{}, seasons, seeds, first)

	fmt.Println("value multiplier: the times an annual's harvest a unit of asparagus")
	fmt.Printf("must be worth for the garden with one asparagus bed to match rotation")
	fmt.Printf(" alone (mean total %.0f). Annual units count 1.\n\n", none)

	fmt.Printf("%-6s", "K")
	for _, gr := range gardens[1:] {
		fmt.Printf(" %11s", gr.name)
	}
	fmt.Println()

	bestK := make([]int, len(gardens)-1)
	bestM := make([]float64, len(gardens)-1)
	for i := range bestK {
		bestM[i] = math.Inf(1)
	}
	for k := 4; k <= 20; k++ {
		fmt.Printf("%-6d", k)
		for i, gr := range gardens[1:] {
			total, asparagus := means(freshGarden(beds, gr.build), sim.Perennial{Seasons: k}, seasons, seeds, first)
			m := multiplier(total, asparagus, none)
			if m < bestM[i] {
				bestK[i], bestM[i] = k, m
			}
			fmt.Printf(" %11.2f", m)
		}
		fmt.Println()
	}

	fmt.Printf("%-6s", "lowest")
	for _, k := range bestK {
		fmt.Printf(" %11d", k)
	}
	fmt.Println()
	fmt.Printf("%-6s", "at")
	for _, m := range bestM {
		fmt.Printf(" %11.2f", m)
	}
	fmt.Println()
}

// multiplier is the value one asparagus unit needs, in annual
// units: at multiplier m the garden's worth is m*asparagus plus the
// annual harvest beside it (withTotal - asparagus), and setting
// that equal to the rotation-only total gives this ratio.
func multiplier(withTotal, asparagus, noneTotal float64) float64 {
	return 1 + (noneTotal-withTotal)/asparagus
}

// meanTotal runs newGarden's strategy over every seed and returns
// the mean garden total harvest.
func meanTotal(newGarden func() sim.Garden, strategy sim.Strategy, seasons, seeds int, first int64) float64 {
	total, _ := means(newGarden, strategy, seasons, seeds, first)
	return total
}

// means runs newGarden's strategy over every seed and returns the
// mean garden total and the mean asparagus share of it — the share
// tallied by Run from each run's own history.
func means(newGarden func() sim.Garden, strategy sim.Strategy, seasons, seeds int, first int64) (total, asparagus float64) {
	for i := 0; i < seeds; i++ {
		res := sim.Run(newGarden(), strategy, seasons, first+int64(i))
		total += float64(res.Total)
		asparagus += float64(res.Asparagus)
	}
	total /= float64(seeds)
	asparagus /= float64(seeds)
	return
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
