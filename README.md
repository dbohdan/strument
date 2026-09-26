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

Built in sessions driven through [Strument](https://dbohdan.com/strument);
see DRIVING.md for notes on those sessions.
