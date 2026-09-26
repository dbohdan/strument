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
