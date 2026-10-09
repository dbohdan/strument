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
//     poor harvest instead of nothing. The one perennial below is
//     the exception.
//
//   - Asparagus is the garden's one perennial, outside the rotation.
//     From crowns it needs two seasons to root before it crops — our
//     establishment — and once established we let it harvest like a
//     well-tended annual bed (BaseYield), because a mature bed is a
//     dependable cropper and we have no reason to invent a premium.
//     It feeds like a brassica: heavy nitrogen for the ferns and
//     potassium for the crowns (Draw 3-1-2), with the cut ferns left
//     on the bed returning a little nitrogen (Give 1-0-0) — and it
//     draws whether or not it crops, since roots are what the
//     establishment seasons are buying. Its fusarium crown rot
//     builds the whole time it stands: three tenths a season,
//     deliberately uncapped, which at BaseYield 10 and pressureCost
//     2 takes the harvest to zero in about eighteen seasons — the
//     15-to-25-year field life growers quote — because real fields
//     decline rather than settling at some floor. Once the planting
//     is moved, fusarium fades from the old bed at the slow
//     persistent rate, the same as clubroot and white rot.
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

// Asparagus is the garden's perennial: planted once, it crops in the
// same bed for years without replanting, outside the committee's
// six-family rotation.
const Asparagus Family = Cucurbits + 1

var familyNames = [...]string{
	"brassicas", "legumes", "roots", "alliums", "nightshades", "cucurbits",
	"asparagus",
}

// String returns the family's name, or "unknown" if f is out of range.
func (f Family) String() string {
	if f < 0 || int(f) >= len(familyNames) {
		return "unknown"
	}
	return familyNames[f]
}

// Crop describes one family: what it draws from the soil over a
// season, what it gives back in residues after the harvest, how its
// pest pressure behaves while it is in and out of the bed, and how
// long it needs to establish before it crops.
type Crop struct {
	Name      string // representative member of the family
	Draw      Nutrients
	Give      Nutrients
	Fade      int // tenths of pressure lost per season out of the bed
	Build     int // tenths of pressure gained per season in the bed
	Cap       int // ceiling on pressure; 0 means no ceiling
	Establish int // seasons of no harvest while the crop roots
}

// Crops maps each family to its soil and pest budget. See the
// package comment for where the numbers come from.
var Crops = map[Family]Crop{
	Brassicas: {
		Name:  "cabbage",
		Draw:  Nutrients{Nitrogen: 3, Phosphorus: 1, Potassium: 2},
		Give:  Nutrients{Nitrogen: 1}, // bulky leaves and roots left to dig in
		Fade:  fadePersistent,         // clubroot spores outlast a two-family cycle
		Build: pressureUnit,
		Cap:   PressureMax,
	},
	Legumes: {
		Name:  "beans",
		Draw:  Nutrients{Phosphorus: 1, Potassium: 1}, // fixes its own nitrogen
		Give:  Nutrients{Nitrogen: 2},                 // ~50-150 lb N/acre/season
		Fade:  fadeOneSeason,
		Build: pressureUnit,
		Cap:   PressureMax,
	},
	Roots: {
		Name:  "carrots",
		Draw:  Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 1},
		Fade:  fadeOneSeason,
		Build: pressureUnit,
		Cap:   PressureMax,
		// lifted whole, almost nothing left behind
	},
	Alliums: {
		Name:  "onions",
		Draw:  Nutrients{Nitrogen: 1, Phosphorus: 1, Potassium: 2},
		Fade:  fadePersistent, // white rot sclerotia last for years
		Build: pressureUnit,
		Cap:   PressureMax,
	},
	Nightshades: {
		Name:  "tomatoes",
		Draw:  Nutrients{Nitrogen: 3, Phosphorus: 2, Potassium: 1},
		Fade:  fadeOneSeason,
		Build: pressureUnit,
		Cap:   PressureMax,
	},
	Cucurbits: {
		Name:  "squash",
		Draw:  Nutrients{Nitrogen: 2, Phosphorus: 1, Potassium: 2},
		Give:  Nutrients{Nitrogen: 1}, // vines left on the bed as mulch
		Fade:  fadeOneSeason,
		Build: pressureUnit,
		Cap:   PressureMax,
	},
	Asparagus: {
		Name:      "asparagus",
		Draw:      Nutrients{Nitrogen: 3, Phosphorus: 1, Potassium: 2}, // feeds like a brassica
		Give:      Nutrients{Nitrogen: 1},                              // cut ferns left as mulch
		Fade:      fadePersistent,                                      // fusarium lives on in the soil
		Build:     buildAsparagus,                                      // crown rot accumulates while it stands
		Establish: 2,                                                   // two seasons rooting before the first crop
		// Cap stays 0: a standing field declines instead of settling.
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

// buildAsparagus is how fast fusarium pressure piles up in a
// standing asparagus bed, in tenths per season, uncapped. At
// BaseYield 10 and pressureCost 2 the harvest reaches zero once
// pressure hits fifty tenths: about eighteen seasons standing,
// against the 15-to-25-year field life growers quote before the
// crowns give out. Annuals build faster but settle at PressureMax;
// a real field declines instead.
const buildAsparagus = 3

// Bed is one garden bed: its soil, the pest and disease pressure
// accumulated against each family, how many seasons each family has
// stood in it without a break (for establishment), and optionally
// its own fade and build rates. Nil Fades and Builds mean the
// package defaults from Crops.
type Bed struct {
	Soil     Nutrients
	Pressure map[Family]int
	Stood    map[Family]int // consecutive seasons stood, ending last season
	Fades    Fades
	Builds   Builds
}

// Fades holds per-family fade rates in tenths of pressure per
// season a family is out of the bed. A bed may carry its own copy —
// see DefaultFades — so a run can explore other rates without
// touching the package defaults.
type Fades map[Family]int

// Builds holds per-family pressure build rates in tenths per season
// a family is in the bed. A bed may carry its own copy for the same
// reason as Fades.
type Builds map[Family]int

// DefaultFades returns the package's rule-of-thumb fade rates for
// every family: fadePersistent for brassicas and alliums,
// fadeOneSeason for the rest.
func DefaultFades() Fades {
	f := make(Fades, len(Crops))
	for family, crop := range Crops {
		f[family] = crop.Fade
	}
	return f
}

// DefaultBuilds returns the package's rule-of-thumb pressure build
// rates for every family.
func DefaultBuilds() Builds {
	b := make(Builds, len(Crops))
	for family, crop := range Crops {
		b[family] = crop.Build
	}
	return b
}

// fade is the rate at which pressure against f fades while f is out
// of the bed: the bed's own rate for f if it sets one, otherwise the
// package default.
func (b Bed) fade(f Family) int {
	if r, ok := b.Fades[f]; ok {
		return r
	}
	return Crops[f].Fade
}

// build is the rate at which pressure against f grows while f is in
// the bed: the bed's own rate for f if it sets one, otherwise the
// package default.
func (b Bed) build(f Family) int {
	if r, ok := b.Builds[f]; ok {
		return r
	}
	return Crops[f].Build
}

// NewBed returns a bed with the given soil and no pest pressure on
// any family.
func NewBed(soil Nutrients) Bed {
	return Bed{
		Soil:     soil,
		Pressure: make(map[Family]int),
		Stood:    make(map[Family]int),
	}
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
//     never goes below zero — and a crop still establishing
//     (Crop.Establish) harvests nothing at all, though it still
//     draws on the soil and its pressure still builds. Moving to a
//     new bed starts the establishment count again.
//
//   - The bed gives up what the crop draws and gains what it gives
//     back, per nutrient, clamped to [0, SoilCap].
//
//   - Pressure against the grown family rises by the bed's build
//     rate for that family each season in the bed — Crop.Build
//     unless the bed carries its own Builds — up to the crop's Cap
//     (PressureMax for the annuals, no ceiling for the perennial);
//     while a family is out of the bed, its pressure fades by the
//     bed's rate for that family — Crop.Fade unless the bed carries
//     its own Fades (rotation starves it; slowly for the persistent
//     diseases). Nothing drops below zero.
func Season(b Bed, family Family) (int, Bed) {
	crop, ok := Crops[family]
	if !ok {
		panic("garden: unknown crop family")
	}

	pressure := b.Pressure[family]
	if crop.Cap > 0 {
		pressure = min(crop.Cap, pressure)
	}
	yield := BaseYield - pressureCost*pressure/pressureUnit - shortfall(crop.Draw, b.Soil)
	if b.Stood[family] < crop.Establish {
		yield = 0 // establishing: roots first, no harvest
	}
	if yield < 0 {
		yield = 0
	}

	after := Bed{
		Soil:     apply(b.Soil, crop),
		Pressure: make(map[Family]int, len(b.Pressure)+1),
		Stood:    map[Family]int{family: b.Stood[family] + 1},
		Fades:    b.Fades,
		Builds:   b.Builds,
	}
	for f, p := range b.Pressure {
		after.Pressure[f] = p
	}
	for f, p := range after.Pressure {
		if f != family {
			after.Pressure[f] = max(0, p-b.fade(f))
		}
	}
	grown := b.Pressure[family] + b.build(family)
	if crop.Cap > 0 {
		grown = min(crop.Cap, grown)
	}
	after.Pressure[family] = grown

	return yield, after
}

// SpreadCompost returns the bed after the committee's spring spread:
// Compost added to each nutrient, up to SoilCap. When to spread is
// the caller's decision — the committee does it every spring.
func SpreadCompost(b Bed) Bed {
	return SpreadCompostWith(b, Compost)
}

// SpreadCompostWith is SpreadCompost with the spring's dressing given
// explicitly, for asking what a different compost would have done.
func SpreadCompostWith(b Bed, dressing Nutrients) Bed {
	b.Soil = add(b.Soil, dressing)
	return b
}

// Rest puts the bed under a cover crop for one season, planted with
// nothing for harvest. It returns a yield of 0; the soil gains Cover,
// more than a spring's compost; pressure against every family fades
// by the bed's rate for that family — the same decay it gets when it
// is rotated out, now applied to all of them at once, which is why
// one rested season barely touches the persistent brassica and
// allium diseases — and nothing stands, so every family's
// establishment count starts over.
func Rest(b Bed) (int, Bed) {
	after := Bed{
		Soil:     add(b.Soil, Cover),
		Pressure: make(map[Family]int, len(b.Pressure)),
		Stood:    make(map[Family]int),
		Fades:    b.Fades,
		Builds:   b.Builds,
	}
	for f, p := range b.Pressure {
		after.Pressure[f] = max(0, p-b.fade(f))
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
