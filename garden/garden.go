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
//   - Pests and diseases build up quickly and fade slowly, at a
//     rate that differs by family. Growing a family adds a whole
//     point of pressure per season, up to PressureMax. While a
//     family is out of the bed its pressure fades at that family's
//     Fade rate: brassicas and alliums carry the famously persistent
//     soil diseases — clubroot and onion white rot are commonly
//     cited as lasting a decade or more in the soil — so theirs
//     fades by only two tenths a season, five seasons (one trip
//     around the six-family rotation) to shed a single point.
//     Everything else is assumed to starve out within a season away:
//     a whole point. This is why two slots aren't enough — under
//     brassica/legume alternation the brassica diseases barely fade
//     between plantings, while six slots clear every family.
//     Pressure stops climbing at PressureMax — the pests that can
//     live in this bed already do — so a monoculture settles at a
//     poor harvest instead of nothing.
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

// Crop describes one family: what it draws from the soil over a
// season, what it gives back in residues after the harvest, and how
// fast its pest pressure fades while it is out of the bed.
type Crop struct {
	Name string // representative member of the family
	Draw Nutrients
	Give Nutrients
	Fade int // tenths of pressure lost per season out of the bed
}

// Crops maps each family to its soil budget. See the package comment
// for where the numbers come from.
var Crops = map[Family]Crop{
	Brassicas: {
		Name: "cabbage",
		Draw: Nutrients{Nitrogen: 3, Phosphorus: 1, Potassium: 2},
		Give: Nutrients{Nitrogen: 1}, // bulky leaves and roots left to dig in
		Fade: fadePersistent,         // clubroot spores outlast a two-family cycle
	},
	Legumes: {
		Name: "beans",
		Draw: Nutrients{Phosphorus: 1, Potassium: 1}, // fixes its own nitrogen
		Give: Nutrients{Nitrogen: 2},                 // ~50-150 lb N/acre/season
		Fade: fadeOneSeason,
	},
	Roots: {
		Name: "carrots",
		Draw: Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 1},
		Fade: fadeOneSeason,
		// lifted whole, almost nothing left behind
	},
	Alliums: {
		Name: "onions",
		Draw: Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 2},
		Fade: fadePersistent, // white rot sclerotia last for years
	},
	Nightshades: {
		Name: "tomatoes",
		Draw: Nutrients{Nitrogen: 3, Phosphorus: 2, Potassium: 1},
		Fade: fadeOneSeason,
	},
	Cucurbits: {
		Name: "squash",
		Draw: Nutrients{Nitrogen: 2, Phosphorus: 1, Potassium: 2},
		Give: Nutrients{Nitrogen: 1}, // vines left on the bed as mulch
		Fade: fadeOneSeason,
	},
}

// BaseYield is what a season harvests with fertile soil and no pest
// pressure.
const BaseYield = 10

// pressureCost is the yield lost per point of pest/disease pressure:
// roughly a fifth of the crop per point.
const pressureCost = 2

// pressureUnit is one whole point of pressure, in the tenths that
// Bed.Pressure counts in. Tenths let the per-family Fade rates below
// stay small integers.
const pressureUnit = 10

// PressureMax is the ceiling on pest and disease pressure, in
// tenths: twenty tenths — two whole points — is as much yield as the
// penalty can take (pressureCost × two points = 4 of BaseYield).
// Pressure climbs a whole point each season a family stays in the
// bed but stops here: past a certain infestation the pests and
// diseases that can live in this bed already do, so a monoculture
// settles at a poor harvest — BaseYield - pressureCost*PressureMax/
// pressureUnit, less any nutrient shortfall — instead of dropping to
// zero and staying there.
const PressureMax = 20

// The per-family Fade rates, in tenths of pressure per season out of
// the bed.
const (
	// fadeOneSeason sheds a whole point per season away: the pest or
	// disease is assumed to starve out without its host within a
	// season or two. This is the usual rotation rule of thumb.
	fadeOneSeason = pressureUnit

	// fadePersistent is for the famously long-lived soil diseases.
	// Clubroot in brassica beds and onion white rot are commonly
	// cited as lasting a decade or more in the soil; at two tenths
	// a season a point needs five seasons away to fade — roughly one
	// trip around the six-family rotation, and more than a
	// two-family alternation ever gives it.
	fadePersistent = 2
)

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
//   - Yield starts at BaseYield, then loses pressureCost for every
//     whole point of pressure the family already carries (tenths
//     round down, so under half a point of pressure is free), and
//     1 point per unit of any nutrient the bed cannot supply. It
//     never goes below zero.
//
//   - The bed gives up what the crop draws and gains what it gives
//     back, per nutrient, clamped to [0, SoilCap].
//
//   - Pressure against the grown family rises by pressureUnit a
//     season, up to PressureMax (pests breed, but only so far);
//     while a family is out of the bed, its pressure fades by that
//     family's Fade rate (rotation starves it — slowly for the
//     persistent diseases). Nothing drops below zero.
func Season(b Bed, family Family) (int, Bed) {
	crop, ok := Crops[family]
	if !ok {
		panic("garden: unknown crop family")
	}

	pressure := min(PressureMax, b.Pressure[family])
	yield := BaseYield - pressureCost*pressure/pressureUnit - shortfall(crop.Draw, b.Soil)
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
		if f != family {
			after.Pressure[f] = max(0, p-Crops[f].Fade)
		}
	}
	after.Pressure[family] = min(PressureMax, b.Pressure[family]+pressureUnit)

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
// more than a spring's compost; and pressure against every family
// fades by that family's Fade rate — the same decay it gets when it
// is rotated out, now applied to all of them at once, which is why
// one rested season barely touches the persistent brassica and
// allium diseases.
func Rest(b Bed) (int, Bed) {
	after := Bed{
		Soil:     add(b.Soil, Cover),
		Pressure: make(map[Family]int, len(b.Pressure)),
	}
	for f, p := range b.Pressure {
		after.Pressure[f] = max(0, p-Crops[f].Fade)
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
