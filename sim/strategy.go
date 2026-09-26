package sim

import "larkspur/garden"

// Rotation follows the committee's rule: the six families in
// rotation order, each bed starting at a different point in the
// cycle — bed 0 with brassicas, bed 1 with legumes, and so on — and
// advancing one place every season.
type Rotation struct{}

func (Rotation) Choose(g Garden, bed, season int) Planting {
	return Use(garden.FamilyOrder[(bed+season)%len(garden.FamilyOrder)])
}

// Monoculture plants one family every season, whatever the bed has
// grown before. The zero value plants brassicas forever.
type Monoculture struct {
	Family garden.Family
}

func (m Monoculture) Choose(g Garden, bed, season int) Planting {
	return Use(m.Family)
}

// Alternation flips between brassicas and legumes every season in
// every bed, starting the first season with brassicas.
type Alternation struct{}

func (Alternation) Choose(g Garden, bed, season int) Planting {
	last, planted := lastPlanted(g.History[bed])
	if planted && last == garden.Brassicas {
		return Use(garden.Legumes)
	}
	return Use(garden.Brassicas)
}

// Greedy gives every family one trial in a bed — brassicas first,
// then in rotation order — and after that replants whichever family
// harvested best in that bed the last time it was grown there. Ties
// go to the family earliest in the rotation.
type Greedy struct{}

func (Greedy) Choose(g Garden, bed, season int) Planting {
	// The most recent harvest of each family in this bed.
	last := make(map[garden.Family]int)
	for _, r := range g.History[bed] {
		if !r.Planting.Rest {
			last[r.Planting.Family] = r.Harvest
		}
	}

	// One trial each first.
	for _, f := range garden.FamilyOrder {
		if _, tried := last[f]; !tried {
			return Use(f)
		}
	}

	best := garden.FamilyOrder[0]
	for _, f := range garden.FamilyOrder[1:] {
		if last[f] > last[best] {
			best = f
		}
	}
	return Use(best)
}

// Perennial keeps one bed of the garden in asparagus while every
// other bed follows Rotation. A planting stands for Seasons seasons
// (the establishment seasons among them), then moves to the bed that
// has gone longest without one — never having had one counts as
// longest, ties to the lowest bed — and the bed it leaves rejoins
// the rotation. Seasons should be at least 2: below that the
// planting is moved before it ever crops.
type Perennial struct {
	Seasons int
}

func (p Perennial) Choose(g Garden, bed, season int) Planting {
	if holder, standing := p.standing(g); standing {
		if bed == holder {
			return Use(garden.Asparagus)
		}
	} else if p.longestAbsent(g) == bed {
		return Use(garden.Asparagus)
	}
	return (Rotation{}).Choose(g, bed, season)
}

// standing returns the bed whose asparagus planting hasn't finished
// its stand yet, if there is one.
func (p Perennial) standing(g Garden) (int, bool) {
	for i, h := range g.History {
		n := 0
		for j := len(h) - 1; j >= 0 && h[j].Planting.Family == garden.Asparagus; j-- {
			n++
		}
		if n > 0 && n < p.Seasons {
			return i, true
		}
	}
	return 0, false
}

// longestAbsent returns the bed whose asparagus season is oldest,
// never having had one counting as oldest.
func (p Perennial) longestAbsent(g Garden) int {
	best, bestLast := 0, lastAsparagus(g.History[0])
	for i := 1; i < len(g.History); i++ {
		if last := lastAsparagus(g.History[i]); last < bestLast {
			best, bestLast = i, last
		}
	}
	return best
}

// lastAsparagus is the season index of h's most recent asparagus
// planting, or -1 if the bed never held one.
func lastAsparagus(h History) int {
	for j := len(h) - 1; j >= 0; j-- {
		if h[j].Planting.Family == garden.Asparagus {
			return j
		}
	}
	return -1
}

// lastPlanted returns the most recently planted family in h and
// whether anything has been planted at all.
func lastPlanted(h History) (garden.Family, bool) {
	for i := len(h) - 1; i >= 0; i-- {
		if !h[i].Planting.Rest {
			return h[i].Planting.Family, true
		}
	}
	return garden.Brassicas, false
}
