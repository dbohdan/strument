package sim

import (
	"reflect"
	"testing"

	"larkspur/garden"
)

// TestRunIsDeterministicForAFixedSeed runs every strategy twice with
// the same seed and requires identical results.
func TestRunIsDeterministicForAFixedSeed(t *testing.T) {
	const seasons, seed = 40, 2024
	tests := []struct {
		name     string
		strategy Strategy
	}{
		{"rotation", Rotation{}},
		{"monoculture", Monoculture{}},
		{"alternation", Alternation{}},
		{"greedy", Greedy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := Run(NewGarden(0), tt.strategy, seasons, seed)
			again := Run(NewGarden(0), tt.strategy, seasons, seed)

			if !reflect.DeepEqual(first, again) {
				t.Errorf("same seed twice gave different runs:\n%v\n%v", first.PerSeason, again.PerSeason)
			}
			sum := 0
			for _, harvest := range first.PerSeason {
				sum += harvest
			}
			if first.Total != sum {
				t.Errorf("total = %d, want the per-season sum %d", first.Total, sum)
			}
		})
	}
}

// TestRunWeatherVaries checks the other half of the weather promise:
// a run is deterministic but not flat — season totals move with the
// year's weather.
func TestRunWeatherVaries(t *testing.T) {
	res := Run(NewGarden(0), Rotation{}, 40, 2024)
	for _, total := range res.PerSeason[1:] {
		if total != res.PerSeason[0] {
			return
		}
	}
	t.Fatalf("all %d seasons harvested %d — the weather never moved the totals",
		len(res.PerSeason), res.PerSeason[0])
}

// TestRunUsesGardenFades: a garden carrying its own fade rates
// reaches the run — nearly permanent brassica and allium pests cut
// into even the six-family rotation, since five seasons away no
// longer clears a point.
func TestRunUsesGardenFades(t *testing.T) {
	fades := garden.DefaultFades()
	fades[garden.Brassicas], fades[garden.Alliums] = 1, 1 // ten seasons to shed a point

	nearly := Run(NewGarden(0).WithFades(fades), Rotation{}, 40, 2024)
	normal := Run(NewGarden(0), Rotation{}, 40, 2024)

	if nearly.Total >= normal.Total {
		t.Errorf("rotation under nearly permanent pests totalled %d, want less than the default run's %d",
			nearly.Total, normal.Total)
	}
}

// restFirst rests every bed in the first season, then rotates.
type restFirst struct{}

func (restFirst) Choose(g Garden, bed, season int) Planting {
	if season == 0 {
		return Rest()
	}
	return Rotation{}.Choose(g, bed, season)
}

// TestRunRests checks that a strategy can rest a bed: the resting
// season harvests nothing.
func TestRunRests(t *testing.T) {
	res := Run(NewGarden(0), restFirst{}, 4, 2024)
	if res.PerSeason[0] != 0 {
		t.Errorf("first season (all rested) harvested %d, want 0", res.PerSeason[0])
	}
}

// TestRunLeavesGardenUntouched checks Run plays on its own copy.
func TestRunLeavesGardenUntouched(t *testing.T) {
	g := NewGarden(0)
	before := clone(g)

	Run(g, Rotation{}, 20, 2024)

	if !reflect.DeepEqual(g, before) {
		t.Error("Run modified the garden it was passed")
	}
}

// TestRotationStrategy checks the committee's rule: bed i starts at
// FamilyOrder[i] and advances one place per season.
func TestRotationStrategy(t *testing.T) {
	g := NewGarden(6)
	for i, f := range garden.FamilyOrder {
		if got := (Rotation{}).Choose(g, i, 0); got.Family != f {
			t.Errorf("bed %d in season 0 plants %s, want %s", i, got.Family, f)
		}
	}
	if got := (Rotation{}).Choose(g, 0, 3); got.Family != garden.Alliums {
		t.Errorf("bed 0 in season 3 plants %s, want alliums", got.Family)
	}
}

// TestMonocultureStrategy checks that monoculture never deviates
// from its family.
func TestMonocultureStrategy(t *testing.T) {
	g := NewGarden(3)
	for _, season := range []int{0, 1, 9} {
		if got := (Monoculture{}).Choose(g, 2, season); got.Family != garden.Brassicas {
			t.Errorf("bed 2 in season %d plants %s, want brassicas", season, got.Family)
		}
	}
}

// TestAlternationStrategy checks that alternation flips between
// brassicas and legumes every season.
func TestAlternationStrategy(t *testing.T) {
	g := NewGarden(1)
	want := []garden.Family{garden.Brassicas, garden.Legumes, garden.Brassicas, garden.Legumes}
	for season, w := range want {
		got := (Alternation{}).Choose(g, 0, season)
		if got.Family != w {
			t.Errorf("season %d plants %s, want %s", season, got.Family, w)
		}
		g.History[0] = append(g.History[0], Record{Planting: got})
	}
}

// TestGreedyStrategy checks greedy's two phases: one trial per
// family in rotation order, then replanting whichever family
// harvested best the last time it was grown.
func TestGreedyStrategy(t *testing.T) {
	g := NewGarden(1)
	harvest := map[garden.Family]int{
		garden.Brassicas:   10,
		garden.Legumes:     40,
		garden.Roots:       20,
		garden.Alliums:     5,
		garden.Nightshades: 30,
		garden.Cucurbits:   15,
	}

	for _, f := range garden.FamilyOrder {
		got := (Greedy{}).Choose(g, 0, 0)
		if got.Family != f {
			t.Fatalf("trial plant %s, want %s — one trial per family, in rotation order", got.Family, f)
		}
		g.History[0] = append(g.History[0], Record{Planting: got, Harvest: harvest[got.Family]})
	}

	if got := (Greedy{}).Choose(g, 0, 6); got.Family != garden.Legumes {
		t.Errorf("after trials plants %s, want legumes (best last harvest, 40)", got.Family)
	}

	// Legumes' last harvest was poor, so greedy moves on to the
	// family whose most recent harvest is now the best.
	g.History[0] = append(g.History[0], Record{Planting: Use(garden.Legumes), Harvest: 2})
	if got := (Greedy{}).Choose(g, 0, 7); got.Family != garden.Nightshades {
		t.Errorf("after a poor legume season plants %s, want nightshades (best last harvest, 30)", got.Family)
	}
}
