package garden

import "testing"

// TestLegumesAfterBrassicasRecoverNitrogen checks the rotation's
// headline claim: legumes put nitrogen back that brassicas stripped.
func TestLegumesAfterBrassicasRecoverNitrogen(t *testing.T) {
	tests := []struct {
		name  string
		start Nutrients
	}{
		{"fertile soil", Nutrients{Nitrogen: 10, Phosphorus: 10, Potassium: 10}},
		{"worn soil", Nutrients{Nitrogen: 4, Phosphorus: 6, Potassium: 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, afterBrassicas := Season(NewBed(tt.start), Brassicas)
			_, afterLegumes := Season(afterBrassicas, Legumes)

			got := afterLegumes.Soil.Nitrogen
			if prev := afterBrassicas.Soil.Nitrogen; got <= prev {
				t.Errorf("nitrogen after legumes = %d, want more than %d left by brassicas", got, prev)
			}
			if got > tt.start.Nitrogen {
				t.Errorf("nitrogen after legumes = %d, want no more than the starting %d", got, tt.start.Nitrogen)
			}
		})
	}
}

// TestSameFamilyTwiceRaisesPressureAndLowersYield checks the cost of
// skipping rotation: pressure builds and the second harvest shrinks.
func TestSameFamilyTwiceRaisesPressureAndLowersYield(t *testing.T) {
	tests := []struct {
		name   string
		family Family
	}{
		{"brassicas", Brassicas},
		{"legumes", Legumes},
		{"alliums", Alliums},
		{"nightshades", Nightshades},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, afterFirst := Season(NewBed(FertileSoil), tt.family)
			second, afterSecond := Season(afterFirst, tt.family)

			if p1, p2 := afterFirst.Pressure[tt.family], afterSecond.Pressure[tt.family]; p2 <= p1 {
				t.Errorf("pressure after second season = %d, want more than %d after the first", p2, p1)
			}
			if second >= first {
				t.Errorf("yield: second season = %d, want less than first = %d", second, first)
			}
		})
	}
}

// TestPressureFadesAtFamilyRates pins the per-family fade: one
// season grown leaves a point of pressure; a fast family is clean
// after a single season away, while brassicas and alliums need five
// to shed the same point — one trip around the rotation.
func TestPressureFadesAtFamilyRates(t *testing.T) {
	tests := []struct {
		name   string
		family Family
		away   int // seasons spent growing something else
		want   int // pressure in tenths when the family comes back
	}{
		{"legumes away one season", Legumes, 1, 0},
		{"nightshades away one season", Nightshades, 1, 0},
		{"brassicas away one season", Brassicas, 1, pressureUnit - fadePersistent},
		{"brassicas away four seasons", Brassicas, 4, pressureUnit - 4*fadePersistent},
		{"brassicas away five seasons", Brassicas, 5, pressureUnit - 5*fadePersistent},
		{"alliums away two seasons", Alliums, 2, pressureUnit - 2*fadePersistent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bed := NewBed(FertileSoil)
			_, bed = Season(bed, tt.family) // pressure builds to a point

			other := Legumes
			if tt.family == Legumes {
				other = Roots
			}
			for i := 0; i < tt.away; i++ {
				_, bed = Season(bed, other)
			}

			if got := bed.Pressure[tt.family]; got != tt.want {
				t.Errorf("%s pressure after %d seasons away = %d tenths, want %d",
					tt.family, tt.away, got, tt.want)
			}
		})
	}
}

// TestBedFades checks that a bed can carry its own fade rates: the
// override drives decay, survives a season and a rest, and leaves
// families it doesn't name on the package default.
func TestBedFades(t *testing.T) {
	bed := NewBed(FertileSoil)
	bed.Fades = Fades{Brassicas: 5} // this bed's own rate

	_, bed = Season(bed, Brassicas) // brassicas build a point
	if bed.Fades[Brassicas] != 5 {
		t.Fatalf("fade rates lost growing a crop: %v", bed.Fades)
	}

	_, bed = Season(bed, Alliums)
	if got := bed.Pressure[Brassicas]; got != pressureUnit-5 {
		t.Errorf("brassicas pressure = %d tenths, want %d (the bed's own rate of 5)", got, pressureUnit-5)
	}

	_, bed = Season(bed, Roots)
	if got := bed.Pressure[Alliums]; got != pressureUnit-fadePersistent {
		t.Errorf("alliums pressure = %d tenths, want %d (package default)", got, pressureUnit-fadePersistent)
	}

	_, rested := Rest(bed)
	if rested.Fades[Brassicas] != 5 {
		t.Errorf("fade rates lost resting the bed: %v", rested.Fades)
	}
}

// TestSpreadCompost checks the committee's spring spread: Compost
// added to each nutrient, never past SoilCap.
func TestSpreadCompost(t *testing.T) {
	tests := []struct {
		name  string
		start Nutrients
		want  Nutrients
	}{
		{"hungry bed", Nutrients{5, 6, 7}, Nutrients{6, 7, 8}},
		{"nearly full bed",
			Nutrients{SoilCap - 1, SoilCap, SoilCap},
			Nutrients{SoilCap, SoilCap, SoilCap}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SpreadCompost(NewBed(tt.start)).Soil; got != tt.want {
				t.Errorf("soil after compost = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestRest checks a season under a cover crop: nothing harvested,
// more restored than compost brings, and every family's pressure
// fading at its own rate.
func TestRest(t *testing.T) {
	bed := NewBed(Nutrients{5, 6, 7})
	bed.Pressure[Brassicas], bed.Pressure[Alliums] = PressureMax, pressureUnit
	bed.Pressure[Legumes], bed.Pressure[Roots] = pressureUnit, 0

	yield, after := Rest(bed)

	if yield != 0 {
		t.Errorf("yield = %d, want 0 — nothing is harvested", yield)
	}
	for _, n := range []struct {
		name           string
		got, start     int
		compostPerUnit int
	}{
		{"nitrogen", after.Soil.Nitrogen, bed.Soil.Nitrogen, Compost.Nitrogen},
		{"phosphorus", after.Soil.Phosphorus, bed.Soil.Phosphorus, Compost.Phosphorus},
		{"potassium", after.Soil.Potassium, bed.Soil.Potassium, Compost.Potassium},
	} {
		if d := n.got - n.start; d <= n.compostPerUnit {
			t.Errorf("rest added %d %s, want more than the %d compost brings", d, n.name, n.compostPerUnit)
		}
	}
	want := map[Family]int{
		Brassicas: PressureMax - fadePersistent, // one rested season sheds only the slow rate
		Alliums:   pressureUnit - fadePersistent,
		Legumes:   0, // a fast family is gone in one season
		Roots:     0,
	}
	for f, w := range want {
		if got := after.Pressure[f]; got != w {
			t.Errorf("pressure on %s after rest = %d, want %d", f, got, w)
		}
	}
	if p := bed.Pressure[Brassicas]; p != PressureMax {
		t.Errorf("caller's bed was mutated: brassica pressure = %d, want %d", p, PressureMax)
	}
}

// TestRotationBeatsFortySeasonsOfBrassicas runs two beds for forty
// seasons, both under the committee's spring compost: one planted
// with brassicas every season, one following the six-family
// rotation. With pressure capped at PressureMax the monoculture
// declines but settles at a poor level — it should finish roughly
// between a third and a half of the rotation's total, not at zero.
func TestRotationBeatsFortySeasonsOfBrassicas(t *testing.T) {
	const seasons = 40

	run := func(plant func(season int) Family) int {
		bed := NewBed(FertileSoil)
		total := 0
		for s := 0; s < seasons; s++ {
			bed = SpreadCompost(bed)
			yield, after := Season(bed, plant(s))
			total += yield
			bed = after
		}
		return total
	}

	repeated := run(func(int) Family { return Brassicas })
	rotated := run(func(s int) Family { return FamilyOrder[s%len(FamilyOrder)] })

	t.Logf("brassicas every season: %d over %d seasons", repeated, seasons)
	t.Logf("six-family rotation:    %d over %d seasons", rotated, seasons)
	t.Logf("rotation advantage:     %d", rotated-repeated)

	if lo, hi := rotated/3, rotated/2; repeated < lo || repeated > hi {
		t.Errorf("brassicas total = %d, want roughly a third to half of the rotation's %d (between %d and %d)",
			repeated, rotated, lo, hi)
	}
}

// TestFamilyOrder checks the six families against the rotation order
// fixed in the README, and that every family has a soil budget.
func TestFamilyOrder(t *testing.T) {
	want := []string{"brassicas", "legumes", "roots", "alliums", "nightshades", "cucurbits"}
	if len(FamilyOrder) != len(want) {
		t.Fatalf("FamilyOrder has %d families, want %d", len(FamilyOrder), len(want))
	}
	for i, f := range FamilyOrder {
		if f.String() != want[i] {
			t.Errorf("FamilyOrder[%d] = %q, want %q", i, f, want[i])
		}
		if _, ok := Crops[f]; !ok {
			t.Errorf("no crop defined for %s", f)
		}
	}
}
