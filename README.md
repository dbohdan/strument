# Larkspur

A crop-rotation simulation for the Larkspur Lane community garden, a garden
that does not exist.

The garden has beds, each planted with one crop family per season. Growing
the same family in the same soil drains the nutrients it feeds on and lets
its pests and diseases build up; rotating through families lets the soil
recover. The garden committee's rule, from its design notes, is a fixed
six-family order — brassicas, legumes, roots, alliums, nightshades,
cucurbits — with resting (a cover crop, no advance) and pinning (perennials
stay put).

The question this program exists to answer: over twenty years, how much does
the committee's rule actually beat lazier strategies?

## Results

The default run compares every strategy on a fresh 12-bed garden for 20
seasons (one per year) across 200 seeds; `p10`/`p90` are the 10th and 90th
percentile of the total across seeds. Command: `go run ./cmd/larkspur`.

```
strategy         mean      p10      p90  % of rotation
rotation         2399     2252     2542           100%
monoculture      1084     1020     1152            45%
alternation      2075     1920     2220            86%
greedy           2155     1968     2316            90%

mean harvest per season
rotation     ████████████████████
monoculture  █▆▅▅▅▅▅▅▅▄▃▃▃▃▃▃▃▃▃▃
alternation  ██▇█▆█▆█▆█▆█▆█▆█▅█▆█
greedy       ███████▇▇▇▇▇▇▇▇▆▇▆▇▆
```

Rotation wins. Monoculture falls off and settles at a poor level,
alternation trails because its brassica seasons still carry pressure, and
greedy recovers after its trial seasons; the sparklines show the same
shapes over time.

The percentiles above can mislead: rotation's `p10` (2252) sits below
greedy's `p90` (2316), which looks as if rotation must lose in some years'
weather. But `p10` and `p90` come from different seeds, and a seed is one
weather sequence every strategy can share. Run them pairwise instead.
Command: `go run ./cmd/larkspur -paired`.

```
strategy       wins   ties losses  worst margin
monoculture     200      0      0  +1114 (seed 106)
alternation     200      0      0  +296 (seed 119)
greedy          200      0      0  +118 (seed 107)
```

Rotation beats every strategy in all 200 seeds on the same weather, never
tying or losing. Its tightest margin anywhere is the +118 against greedy in
seed 107: even greedy's luckiest weather leaves rotation ahead. The
overlapping percentiles were an artifact of comparing different weather.

Could 200 of 200 have come out any other way? For two of the three, no.
Rotation never reads a history, and neither does monoculture nor
alternation: their plantings are fixed by bed and season, so their
pre-weather yields are the same on every seed. The only randomness in a
run is the weather sequence, and one seed shares it across every
strategy. So for those pairs the comparison depends on the weather alone,
and it can be checked exhaustively rather than sampled: give each of the
20 seasons, independently, the factor that hurts rotation most — all
3^20 sequences. Rotation still wins, by +922 against monoculture and
+296 against alternation. It would be 200 of 200 for any seeds drawn.
The bound is tight: seed 119's weather really is the worst case for
alternation, which is why its observed margin is also exactly +296.

Greedy is the exception that can differ: it replants from weather-scaled
harvest history, so its plantings — and its pre-weather yields — move
with the seed, and a fixed-yields argument can't cover it. Instead check
it seed by seed: against the plantings greedy actually made on each of
the 200 seeds, even the weather sequence that hurts rotation most still
leaves rotation ahead, by at least +116 (seed 107, the same seed as the
observed +118). So on every seed drawn, no weather was load-bearing;
greedy would need a different run of plantings and its luckiest weather
together. Both checks are tests:
`go test ./sim -run 'Invariant|Varies|WorstWeather' -v`.

### Does the order matter?

The rule fixes the rotation; it also fixes the order: brassicas,
legumes, roots, alliums, nightshades, cucurbits. Common advice puts a
heavy feeder right after legumes to use their nitrogen — the
committee's order puts brassicas right before them. Of the 120
distinct cyclic orders (brassicas fixed first, the other five
permuted), where does the committee's rank? `go run ./cmd/larkspur
-order` ranks all of them on shared seeds, so a difference between two
orders is not weather. The expectation, from the model's own rules:
order should matter very little, and the committee should land
mid-pack.

```
committee: brassicas-legumes-roots-alliums-nightshades-cucurbits
rank 62 of 120, mean 2399

rank mean wins  order
   1  2402   200  brassicas-legumes-cucurbits-roots-alliums-nightshades
   2  2402   200  brassicas-nightshades-cucurbits-roots-alliums-legumes
...
 119  2397     0  brassicas-roots-legumes-nightshades-alliums-cucurbits
 120  2397     0  brassicas-legumes-nightshades-roots-alliums-cucurbits
```

(The program prints the five best and five worst.) The expectation
held: dead middle, and the whole spread across all 120 orders is five
units in 2400 — the committee is three below the best and two above
the worst. The model's rules explain why: every cyclic order has the
same six-season cycle, so pest pressure is order-independent, and
nitrogen never runs short in any order (its per-cycle net is zero and
beds stay near 7-12), so the nitrogen story behind the advice has no
bite here. What little separates the orders is potassium running out
near the end of the run: all five best orders end their cycle with a
light potassium-feeder (legumes, roots, nightshades), four of the five
worst end it with a heavy one (alliums or cucurbits) — and the
committee's order ends with cucurbits. The wins column is 200 or 0:
on shared seeds these differences hold in every seed drawn.

One number in the model is a guess: how fast pest pressure fades while a
family is out of the bed. The default is 0.2 points per season for the
persistent brassica and allium diseases. The sweep reruns the whole
comparison across the plausible range, from nearly permanent to a full
point per season. Command: `go run ./cmd/larkspur -sweep`.

```
fade/season     rotation  monoculture  alternation       greedy        first
        0.1         100%          47%          91%          93%     rotation
        0.2         100%          45%          86%          90%     rotation
        0.3         100%          45%          87%          90%     rotation
        0.5         100%          45%          87%          90%     rotation
        0.7         100%          45%          92%          90%     rotation
        1.0         100%          45%         100%          90%  alternation
```

The reading: the six-slot rule wins only because brassica and allium
diseases persist in the soil. At no persistence (1.0, the rate the fast
families already get) it ties alternation — the last column names the
higher mean, but a few parts in a thousand against percentile spreads of
hundreds is a tie. The exact margin, 86% or 91%, depends on a rate we
cannot measure from here. The curve is stepwise rather than smooth
because the arithmetic is integers: the yield penalty floors to whole
points.

### Perennials: how long should asparagus stand?

The committee pins perennials, and asparagus is the hard case: two seasons
with no harvest while the crowns establish, then a crop every season, while
fusarium crown rot builds in the bed with no rotation to clear it. At some
point the planting has to move, and every move pays the two barren seasons
again. `go run ./cmd/larkspur -perennial` sweeps the stand length K; one bed
holds asparagus, and when K is up it moves to the bed that has gone longest
without it. The fusarium rate is swept too, from a 27-season field life to a
14-season one.

The asparagus constants were committed before the sweep first ran
(`5215019`, then `e91209e`), after the fade rate above had turned out to be
fitted to its answer.

```
K        build 0.2   build 0.3   build 0.4
4             2283        2277        2272
7             2310        2298        2287
10            2310        2292        2273
13            2306        2286        2271
16            2285        2264        2252
20            2276        2255        2244
```

(Every fourth row shown; the program prints K from 4 to 20.) Move it every
five to thirteen seasons: that range is flat to within about 1%, and the peak
is at K = 7 for every build rate. Past fourteen seasons the decline is steady.
The rate moves the level, not the peak.

What the table cannot say is whether asparagus is worth growing. The rotation
with no asparagus totals 2399, above every cell, but that is by construction:
the model gives established asparagus the same harvest per season as an annual,
so two barren seasons and a slow decline can only lose. Whether an asparagus
season is worth more than a cabbage season is a question for the committee, not
the soil.

So the model now answers that as far as it can: how much would a unit of
asparagus have to be worth, in units of an annual's harvest, for the garden
with an asparagus bed to match the garden without one?
`go run ./cmd/larkspur -breakeven` prints that multiplier for every stand
length and fusarium rate. It needs no prices, only the totals the simulation
already produces.

```
K        build 0.2   build 0.3   build 0.4
4             2.27        2.42        2.57
7             1.75        1.94        2.17
10            1.72        2.02        2.44
13            1.84        2.24        2.67
16            2.21        2.87        3.43
20            2.58        3.59        4.44
lowest          10           7           7
at            1.72        1.94        2.17
```

At the committed rate, asparagus pays for its bed if a harvest of it is worth
about twice a harvest of cabbage, and it has to be moved about every seven
seasons for that to hold. Across the plausible range of the disease it needs
1.7 to 2.2 times. Leave it standing past fourteen seasons and the needed
premium climbs quickly. The slowest disease rate puts the cheapest stand at ten
seasons rather than seven: the garden totals are nearly tied there, and the
longer stand grows more asparagus to spread its cost over. Whether asparagus
is worth twice cabbage at market is a question for the committee's accounts,
and for a price list this model does not have.

Built in sessions driven through [Strument](https://dbohdan.com/strument);
see DRIVING.md for notes on those sessions.
