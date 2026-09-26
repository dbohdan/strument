// Package sim runs planting strategies against a whole garden.
//
// A Garden is several beds — twelve by default — that share one
// weather. Each season Run draws a garden-wide factor from the run's
// seed: a bad year, a normal one, or a good one. The factor scales
// every bed's harvest that season, so a run is always the same for a
// given seed but its seasons aren't all the same number.
//
// The weather split is our own rough choice, the sort of spread a
// temperate garden sees year to year: about a quarter of seasons are
// bad (70% of a normal harvest), half are normal, a quarter are good
// (130%). Like the yield arithmetic in package garden it isn't taken
// from any source.
//
// The committee's spring compost is spread on every bed at the start
// of every season, planted or rested. Run leaves the garden it is
// passed untouched; it plays on its own copy.
package sim

import (
	"math/rand"

	"larkspur/garden"
)

// DefaultBeds is how many beds a garden has unless asked for
// otherwise.
const DefaultBeds = 12

// Garden is several beds plus each bed's record of past seasons.
// History stays parallel to Beds: History[i] belongs to Beds[i].
type Garden struct {
	Beds    []garden.Bed
	History []History
}

// History is one bed's record of past seasons, oldest first.
type History []Record

// Record is one past season in one bed: what was planted there and
// what the bed actually harvested after weather.
type Record struct {
	Planting Planting
	Harvest  int
}

// Planting is what a strategy picks for a bed in one season: a crop
// family, or a rest under cover. When Rest is set, Family is ignored.
type Planting struct {
	Family garden.Family
	Rest   bool
}

// Use is the planting of one family.
func Use(f garden.Family) Planting { return Planting{Family: f} }

// Rest is the planting of nothing: a season under a cover crop.
func Rest() Planting { return Planting{Rest: true} }

// NewGarden returns a garden of n beds, all starting at
// garden.FertileSoil with empty histories. Any n <= 0 means
// DefaultBeds.
func NewGarden(n int) Garden {
	if n <= 0 {
		n = DefaultBeds
	}
	g := Garden{
		Beds:    make([]garden.Bed, n),
		History: make([]History, n),
	}
	for i := range g.Beds {
		g.Beds[i] = garden.NewBed(garden.FertileSoil)
	}
	return g
}

// Strategy decides what each bed is planted with, season by season.
// The garden it sees is the run as of the end of last season:
// g.History[bed] is everything that has happened in that bed so far.
type Strategy interface {
	Choose(g Garden, bed, season int) Planting
}

// Result is a run's harvest: one total per season, plus the sum
// over the whole run.
type Result struct {
	PerSeason []int
	Total     int
}

// Run plays seasons of g under strategy s, drawing each season's
// weather from seed. Every bed gets the committee's spring compost,
// then whatever s chooses for it — a family or a rest — and the
// harvest is scaled by that season's garden-wide weather. The garden
// passed in is left untouched.
func Run(g Garden, s Strategy, seasons int, seed int64) Result {
	g = clone(g)
	rng := rand.New(rand.NewSource(seed))
	res := Result{PerSeason: make([]int, 0, max(seasons, 0))}

	for season := 0; season < seasons; season++ {
		weather := drawWeather(rng)

		// Everyone chooses from the garden as it stood at the end
		// of last season; beds are harvested only after all have
		// chosen.
		plantings := make([]Planting, len(g.Beds))
		for i := range g.Beds {
			plantings[i] = s.Choose(g, i, season)
		}

		total := 0
		for i, planting := range plantings {
			g.Beds[i] = garden.SpreadCompost(g.Beds[i])

			var yield int
			if planting.Rest {
				yield, g.Beds[i] = garden.Rest(g.Beds[i])
			} else {
				yield, g.Beds[i] = garden.Season(g.Beds[i], planting.Family)
			}

			harvest := yield * weather / 100
			g.History[i] = append(g.History[i], Record{Planting: planting, Harvest: harvest})
			total += harvest
		}

		res.PerSeason = append(res.PerSeason, total)
		res.Total += total
	}
	return res
}

// drawWeather rolls the season's garden-wide weather in percent of a
// normal harvest: a bad year, a normal one, or a good one.
func drawWeather(rng *rand.Rand) int {
	switch roll := rng.Intn(100); {
	case roll < 25: // a bad year: about a quarter of seasons
		return 70
	case roll < 75: // a normal year: about half
		return 100
	default: // a good year: about a quarter
		return 130
	}
}

// clone copies g deeply enough that Run can play on it without
// disturbing the caller's garden, making sure there is a history for
// every bed.
func clone(g Garden) Garden {
	out := Garden{
		Beds:    append([]garden.Bed(nil), g.Beds...),
		History: make([]History, len(g.History)),
	}
	for i, h := range g.History {
		out.History[i] = append(History(nil), h...)
	}
	for len(out.History) < len(out.Beds) {
		out.History = append(out.History, nil)
	}
	return out
}
