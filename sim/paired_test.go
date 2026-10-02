package sim

import (
	"math"
	"testing"
)

// The only randomness in a run is the weather, and on a shared seed
// every strategy gets the same weather. What can differ is each
// strategy's pre-weather yields: rotation, monoculture and
// alternation choose without ever reading a harvest, so theirs are
// the same on every seed; greedy replants from weather-scaled
// history, so its pre-weather yields move with the seed.

// TestPreWeatherSeedInvariant: three of the four strategies make
// identical plantings on every seed, so their pre-weather garden
// totals per season are fixed — nothing about the seed can change
// them.
func TestPreWeatherSeedInvariant(t *testing.T) {
	const seasons, seeds = 20, 200
	for _, st := range []struct {
		name string
		s    Strategy
	}{
		{"rotation", Rotation{}},
		{"monoculture", Monoculture{}},
		{"alternation", Alternation{}},
	} {
		ref := Run(NewGarden(0), st.s, seasons, 0).RawPerSeason
		for seed := 1; seed < seeds; seed++ {
			if !equal(ref, Run(NewGarden(0), st.s, seasons, int64(seed)).RawPerSeason) {
				t.Fatalf("%s's pre-weather harvest differs on seed %d", st.name, seed)
			}
		}
	}
}

// TestGreedyRawVaries: greedy reads weather-scaled history, so
// unlike the others its pre-weather harvest sequence changes with
// the seed.
func TestGreedyRawVaries(t *testing.T) {
	const seasons, seeds = 20, 200
	first := Run(NewGarden(0), Greedy{}, seasons, 0).RawPerSeason
	for seed := 1; seed < seeds; seed++ {
		if !equal(first, Run(NewGarden(0), Greedy{}, seasons, int64(seed)).RawPerSeason) {
			return
		}
	}
	t.Fatalf("greedy's pre-weather harvest is identical across all %d seeds — it should react to weather-scaled history", seeds)
}

// TestRotationWinsUnderWorstWeather: for the seed-invariant
// strategies the paired 200/200 is forced if rotation still wins
// under the weather that suits it least — every one of the 3^20
// sequences. Greedy is checked seed by seed against the plantings
// it actually made, since a different weather would make it plant
// differently.
func TestRotationWinsUnderWorstWeather(t *testing.T) {
	const seasons, seeds = 20, 200

	rot := Run(NewGarden(0), Rotation{}, seasons, 0)
	for _, other := range []struct {
		name string
		s    Strategy
	}{
		{"monoculture", Monoculture{}},
		{"alternation", Alternation{}},
	} {
		m := WorstWeatherMargin(rot.History, Run(NewGarden(0), other.s, seasons, 0).History)
		if m <= 0 {
			t.Errorf("weather could flip rotation against %s: worst-case margin %+d", other.name, m)
		}
		t.Logf("worst weather margin against %s: %+d", other.name, m)
	}

	// rot is seed-invariant (TestPreWeatherSeedInvariant), so the
	// seed-0 run stands for every seed's rotation side.
	worst, worstSeed := 1<<30, 0
	for seed := 0; seed < seeds; seed++ {
		g := Run(NewGarden(0), Greedy{}, seasons, int64(seed))
		m := WorstWeatherMargin(rot.History, g.History)
		if m <= 0 {
			t.Errorf("seed %d: weather could flip rotation against greedy's plantings: %+d", seed, m)
		}
		if m < worst {
			worst, worstSeed = m, seed
		}
	}
	t.Logf("smallest worst-weather margin against greedy: %+d (seed %d)", worst, worstSeed)
}

// weatherFactors are the season multipliers drawWeather returns.
var weatherFactors = []int{70, 100, 130}

// WorstWeatherMargin is rot's smallest total margin against other
// over every weather sequence a run can produce, holding both
// histories' pre-weather yields fixed. Weather is drawn
// independently each season from {70, 100, 130} and scales every
// bed alike, so the total margin is a sum of per-season terms, each
// depending on one season's factor alone: the minimum over whole
// sequences is the sum of the per-season minima.
func WorstWeatherMargin(rot, other []History) int {
	margin := 0
	for s := 0; s < len(rot[0]); s++ {
		worst := math.MaxInt
		for _, w := range weatherFactors {
			season := 0
			for bed := range rot {
				// Run scales and floors each bed's yield on its own.
				season += rot[bed][s].Raw*w/100 - other[bed][s].Raw*w/100
			}
			if season < worst {
				worst = season
			}
		}
		margin += worst
	}
	return margin
}

// TestWorstWeatherMargin checks the bound the README leans on with a
// case small enough to work out by hand: one bed, two seasons,
// pre-weather yields chosen so the per-season minimum over
// {70, 100, 130} is arithmetic rather than simulation.
func TestWorstWeatherMargin(t *testing.T) {
	// Season 0: rotation 10, other 8. term(w) = floor(10w/100) -
	// floor(8w/100): 70 -> 7-5 = 2, 100 -> 2, 130 -> 13-10 = 3. Min 2.
	// Season 1: rotation 8, other 10. 70 -> 5-7 = -2, 100 -> -2,
	// 130 -> 10-13 = -3. Min -3. Worst total: 2 + -3 = -1.
	rot := []History{{{Raw: 10}, {Raw: 8}}}
	other := []History{{{Raw: 8}, {Raw: 10}}}

	if got := WorstWeatherMargin(rot, other); got != -1 {
		t.Errorf("WorstWeatherMargin = %d, want -1 (season minima 2 and -3)", got)
	}

	// The bound must be attainable: (70, 130) is the sequence most
	// hostile to rotation here and gives exactly -1, while normal
	// weather (100, 100) gives 0, above it.
	if got := rot[0][0].Raw*70/100 - other[0][0].Raw*70/100 +
		rot[0][1].Raw*130/100 - other[0][1].Raw*130/100; got != -1 {
		t.Errorf("hostile sequence margin = %d, want -1 — the bound should be attainable", got)
	}
	if got := rot[0][0].Raw - other[0][0].Raw + rot[0][1].Raw - other[0][1].Raw; got != 0 {
		t.Errorf("normal-weather margin = %d, want 0 — above the bound", got)
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
