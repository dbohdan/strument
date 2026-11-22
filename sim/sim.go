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
	// Dressing, if set, replaces the committee's spring compost
	// (garden.Compost) on every bed. Nil means the default.
	Dressing *garden.Nutrients
	// Spread, if positive, is the fraction of each bed's pressure
	// for each family that passes to each touching neighbour at the
	// end of a season. Zero means no spread between beds.
	Spread float64
}

// History is one bed's record of past seasons, oldest first.
type History []Record

// Record is one past season in one bed: what was planted there, the
// yield before weather scaled it, and what the bed actually
// harvested after weather.
type Record struct {
	Planting Planting
	Raw      int // yield before weather
	Harvest  int
	// Potassium is the bed's potassium after the season's draw and
	// give, before the next spring's dressing: what a short bed has
	// left to offer the next crop.
	Potassium int
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

// WithFades returns a copy of the garden whose beds all use the
// given fade rates (see garden.Fades). The same map backs every
// bed, so treat it as read-only.
func (g Garden) WithFades(f garden.Fades) Garden {
	out := clone(g)
	for i := range out.Beds {
		out.Beds[i].Fades = f
	}
	return out
}

// WithDressing returns a copy of the garden whose spring spread is
// d instead of the committee's compost, on every bed.
func (g Garden) WithDressing(d garden.Nutrients) Garden {
	out := clone(g)
	out.Dressing = &d
	return out
}

// WithSpread returns a copy of the garden whose beds pass fraction
// of their pressure for each family each season to each touching
// neighbour. Zero means no spread, the default, and leaves every
// other output exactly as it was.
//
// 0.5 is not modest — a middle bed gives half its pressure to each
// side, all of it each season. It is not a measurement either; it is
// the smallest fraction that actually moves yields in the model
// (at 0.1, pressure moves by 1 tenth per neighbour but the yield
// penalty floors to the same whole points, so every output is
// identical to no spread). It is chosen so the spread is real, not
// so the model matches any known biology.
func (g Garden) WithSpread(f float64) Garden {
	out := clone(g)
	out.Spread = f
	return out
}

// WithBuilds returns a copy of the garden whose beds all use the
// given pressure build rates (see garden.Builds). The same map
// backs every bed, so treat it as read-only.
func (g Garden) WithBuilds(b garden.Builds) Garden {
	out := clone(g)
	for i := range out.Beds {
		out.Beds[i].Builds = b
	}
	return out
}

// Strategy decides what each bed is planted with, season by season.
// The garden it sees is the run as of the end of last season:
// g.History[bed] is everything that has happened in that bed so far.
type Strategy interface {
	Choose(g Garden, bed, season int) Planting
}

// Result is a run's harvest: one total per season, the same totals
// before weather scales them, their sums, the asparagus share, and
// the run's full per-bed history. The share and RawPerSeason are
// tallied from the yields the model produced — nothing here changes
// how yields are computed.
type Result struct {
	PerSeason    []int
	RawPerSeason []int // garden total per season, before weather
	Total        int
	Asparagus    int
	History      []History // each bed's records, including Raw
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
		raw := 0
		for i, planting := range plantings {
			if g.Dressing != nil {
				g.Beds[i] = garden.SpreadCompostWith(g.Beds[i], *g.Dressing)
			} else {
				g.Beds[i] = garden.SpreadCompost(g.Beds[i])
			}

			var yield int
			if planting.Rest {
				yield, g.Beds[i] = garden.Rest(g.Beds[i])
			} else {
				yield, g.Beds[i] = garden.Season(g.Beds[i], planting.Family)
			}

			raw += yield
			harvest := yield * weather / 100
			g.History[i] = append(g.History[i], Record{
				Planting:  planting,
				Raw:       yield,
				Harvest:   harvest,
				Potassium: g.Beds[i].Soil.Potassium,
			})
			total += harvest
		}

		spreadPressure(g)

		res.PerSeason = append(res.PerSeason, total)
		res.RawPerSeason = append(res.RawPerSeason, raw)
		res.Total += total
	}
	res.History = g.History
	for _, h := range g.History {
		for _, r := range h {
			if !r.Planting.Rest && r.Planting.Family == garden.Asparagus {
				res.Asparagus += r.Harvest
			}
		}
	}
	return res
}

// spreadPressure moves a fraction of each bed's pressure to the beds
// touching it on either side, once per season at the end of it. A bed
// passes the fraction of its pressure for each family to each side it
// has — a middle bed loses it to both neighbours, an end bed to one —
// and gains it back from whatever its neighbours pass. Pressure stays
// in tenths, so each pass floors.
func spreadPressure(g Garden) {
	if g.Spread <= 0 {
		return
	}
	n := len(g.Beds)
	deltas := make([]map[garden.Family]int, n)
	for i := range g.Beds {
		deltas[i] = make(map[garden.Family]int, len(g.Beds[i].Pressure))
	}
	for i := range g.Beds {
		for family, p := range g.Beds[i].Pressure {
			amount := int(float64(p) * g.Spread)
			if amount <= 0 {
				continue
			}
			sides := 0
			if i > 0 {
				deltas[i-1][family] += amount
				sides++
			}
			if i < n-1 {
				deltas[i+1][family] += amount
				sides++
			}
			deltas[i][family] -= amount * sides
		}
	}
	for i := range g.Beds {
		if len(deltas[i]) == 0 {
			continue
		}
		pressure := make(map[garden.Family]int, len(g.Beds[i].Pressure))
		for f, p := range g.Beds[i].Pressure {
			np := p + deltas[i][f]
			if np > 0 {
				pressure[f] = np
			}
		}
		// A family the bed never had pressure for can land there from a
		// neighbour.
		for f, d := range deltas[i] {
			if _, ok := g.Beds[i].Pressure[f]; !ok && d > 0 {
				pressure[f] = d
			}
		}
		g.Beds[i].Pressure = pressure
	}
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
		Beds:     append([]garden.Bed(nil), g.Beds...),
		History:  make([]History, len(g.History)),
		Dressing: g.Dressing,
		Spread:   g.Spread,
	}
	for i, h := range g.History {
		out.History[i] = append(History(nil), h...)
	}
	for len(out.History) < len(out.Beds) {
		out.History = append(out.History, nil)
	}
	return out
}
