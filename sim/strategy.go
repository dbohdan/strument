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
