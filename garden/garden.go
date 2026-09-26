// Package garden models the soil of a single bed at the Larkspur Lane
// community garden: its nitrogen, phosphorus and potassium, and the
// pest and disease pressure each crop family builds up in it.
//
// Everything is a small integer. The numbers are rough rules of thumb,
// not measurements:
//
//   - Which families are heavy or light feeders follows the usual
//     cooperative-extension gardening advice: brassicas, nightshades
//     and cucurbits are heavy feeders; root crops and alliums are
//     modest ones.
//
//   - Legumes fix their own nitrogen, so they draw none from the soil.
//     A legume cover crop is commonly credited with roughly 50-150 lb
//     of nitrogen per acre per season; we round that to 2 points
//     returned to the bed.
//
//   - Pests and diseases that attack a single family (clubroot in
//     brassica soil, onion root rot in allium beds) multiply while
//     that family stays in the bed and starve when it is rotated out:
//     1 point of pressure gained per season grown, 1 lost per season
//     it isn't in the bed.
//
//   - Soil comes back two ways. The committee spreads a small fixed
//     amount of compost on every bed every spring (Compost), and a
//     season left under a cover crop (Rest) harvests nothing,
//     restores more than that compost, and lets pressure against
//     every family fall. Soil never climbs past SoilCap, so a bed
//     can't be composted without bound.
//
//   - The yield arithmetic (BaseYield of 10, 2 points of yield lost
//     per point of pressure, 1 point per unit of nutrient the bed
//     cannot supply) is our own crude scaling so that rotation and
//     fertile soil visibly matter. It isn't taken from any source.
package garden

// Nutrients are the three soil nutrients this simulation tracks, in
// small integer "points".
type Nutrients struct {
	Nitrogen   int
	Phosphorus int
	Potassium  int
}

// FertileSoil is the ordinary starting point: ten points of each
// nutrient, enough for a couple of seasons of any family.
var FertileSoil = Nutrients{Nitrogen: 10, Phosphorus: 10, Potassium: 10}

// SoilCap is the most of any one nutrient a bed can hold. Whatever is
// spread beyond it is assumed to leach or burn off.
const SoilCap = 20

// Compost is what the committee spreads every spring: a small fixed
// amount of each nutrient. See SpreadCompost.
var Compost = Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 1}

// Cover is what a season under a cover crop leaves in the soil —
// more than a spring's worth of compost. See Rest.
var Cover = Nutrients{Nitrogen: 2, Phosphorus: 2, Potassium: 2}

// Family identifies one of the six crop families the committee
// rotates through.
type Family int

// The six families, in the rotation order fixed by the README:
// brassicas, legumes, roots, alliums, nightshades, cucurbits.
const (
	Brassicas Family = iota
	Legumes
	Roots
	Alliums
	Nightshades
	Cucurbits
)

// FamilyOrder is the committee's fixed six-family rotation, in order.
var FamilyOrder = [...]Family{
	Brassicas, Legumes, Roots, Alliums, Nightshades, Cucurbits,
}

var familyNames = [...]string{
	"brassicas", "legumes", "roots", "alliums", "nightshades", "cucurbits",
}

// String returns the family's name, or "unknown" if f is out of range.
func (f Family) String() string {
	if f < 0 || int(f) >= len(familyNames) {
		return "unknown"
	}
	return familyNames[f]
}

// Crop describes what one family takes from the soil over a season
// and what it gives back in residues after the harvest.
type Crop struct {
	Name string // representative member of the family
	Draw Nutrients
	Give Nutrients
}

// Crops maps each family to its soil budget. See the package comment
// for where the numbers come from.
var Crops = map[Family]Crop{
	Brassicas: {
		Name: "cabbage",
		Draw: Nutrients{Nitrogen: 3, Phosphorus: 1, Potassium: 2},
		Give: Nutrients{Nitrogen: 1}, // bulky leaves and roots left to dig in
	},
	Legumes: {
		Name: "beans",
		Draw: Nutrients{Phosphorus: 1, Potassium: 1}, // fixes its own nitrogen
		Give: Nutrients{Nitrogen: 2},                 // ~50-150 lb N/acre/season
	},
	Roots: {
		Name: "carrots",
		Draw: Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 1},
		// lifted whole, almost nothing left behind
	},
	Alliums: {
		Name: "onions",
		Draw: Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 2},
	},
	Nightshades: {
		Name: "tomatoes",
		Draw: Nutrients{Nitrogen: 3, Phosphorus: 2, Potassium: 1},
	},
	Cucurbits: {
		Name: "squash",
		Draw: Nutrients{Nitrogen: 2, Phosphorus: 1, Potassium: 2},
		Give: Nutrients{Nitrogen: 1}, // vines left on the bed as mulch
	},
}

// BaseYield is what a season harvests with fertile soil and no pest
// pressure.
const BaseYield = 10

// pressureCost is the yield lost per point of pest/disease pressure:
// roughly a fifth of the crop per point.
const pressureCost = 2

// Bed is one garden bed: its soil and the pest and disease pressure
// accumulated against each family.
type Bed struct {
	Soil     Nutrients
	Pressure map[Family]int
}

// NewBed returns a bed with the given soil and no pest pressure on
// any family.
func NewBed(soil Nutrients) Bed {
	return Bed{Soil: soil, Pressure: make(map[Family]int)}
}

// Season grows family in bed for one season. It returns the harvest
// yield and the bed as it looks after the harvest; the caller's bed is
// left untouched.
//
// The rules, in full:
//
//   - Yield starts at BaseYield, then loses pressureCost per point of
//     pressure the family already carries, and 1 point per unit of any
//     nutrient the bed cannot supply. It never goes below zero.
//
//   - The bed gives up what the crop draws and gains what it gives
//     back, per nutrient, clamped to [0, SoilCap].
//
//   - Pressure against the grown family rises by 1 (pests breed);
//     pressure against every other family falls by 1 (rotation starves
//     them). Nothing drops below zero.
func Season(b Bed, family Family) (int, Bed) {
	crop, ok := Crops[family]
	if !ok {
		panic("garden: unknown crop family")
	}

	yield := BaseYield - pressureCost*b.Pressure[family] - shortfall(crop.Draw, b.Soil)
	if yield < 0 {
		yield = 0
	}

	after := Bed{
		Soil:     apply(b.Soil, crop),
		Pressure: make(map[Family]int, len(b.Pressure)+1),
	}
	for f, p := range b.Pressure {
		after.Pressure[f] = p
	}
	for f, p := range after.Pressure {
		if f != family && p > 0 {
			after.Pressure[f] = p - 1
		}
	}
	after.Pressure[family] = b.Pressure[family] + 1

	return yield, after
}

// SpreadCompost returns the bed after the committee's spring spread:
// Compost added to each nutrient, up to SoilCap. When to spread is
// the caller's decision — the committee does it every spring.
func SpreadCompost(b Bed) Bed {
	b.Soil = add(b.Soil, Compost)
	return b
}

// Rest puts the bed under a cover crop for one season, planted with
// nothing for harvest. It returns a yield of 0; the soil gains Cover,
// more than a spring's compost; and pest pressure against every
// family falls by 1 — the same decay a family gets when it is rotated
// out, now applied to all of them at once.
func Rest(b Bed) (int, Bed) {
	after := Bed{
		Soil:     add(b.Soil, Cover),
		Pressure: make(map[Family]int, len(b.Pressure)),
	}
	for f, p := range b.Pressure {
		if p > 0 {
			p--
		}
		after.Pressure[f] = p
	}
	return 0, after
}

// shortfall reports how much of the crop's meal the bed cannot supply:
// one yield point lost per missing unit of nutrient.
func shortfall(draw, soil Nutrients) int {
	return max(0, draw.Nitrogen-soil.Nitrogen) +
		max(0, draw.Phosphorus-soil.Phosphorus) +
		max(0, draw.Potassium-soil.Potassium)
}

// apply returns the soil after one season of the crop: give added,
// draw removed, clamped to [0, SoilCap].
func apply(soil Nutrients, crop Crop) Nutrients {
	return clamp(Nutrients{
		Nitrogen:   soil.Nitrogen + crop.Give.Nitrogen - crop.Draw.Nitrogen,
		Phosphorus: soil.Phosphorus + crop.Give.Phosphorus - crop.Draw.Phosphorus,
		Potassium:  soil.Potassium + crop.Give.Potassium - crop.Draw.Potassium,
	})
}

// add returns the soil with extra mixed in, clamped to [0, SoilCap].
func add(soil, extra Nutrients) Nutrients {
	return clamp(Nutrients{
		Nitrogen:   soil.Nitrogen + extra.Nitrogen,
		Phosphorus: soil.Phosphorus + extra.Phosphorus,
		Potassium:  soil.Potassium + extra.Potassium,
	})
}

// clamp keeps each nutrient within [0, SoilCap].
func clamp(n Nutrients) Nutrients {
	return Nutrients{
		Nitrogen:   min(SoilCap, max(0, n.Nitrogen)),
		Phosphorus: min(SoilCap, max(0, n.Phosphorus)),
		Potassium:  min(SoilCap, max(0, n.Potassium)),
	}
}
