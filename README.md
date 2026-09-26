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

Built in sessions driven through [Strument](https://dbohdan.com/strument);
see DRIVING.md for notes on those sessions.
