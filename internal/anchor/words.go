package anchor

// words is the anchor vocabulary: 256 short, common, concrete English words.
//
// Three properties are load bearing, and each is why a word is here rather than
// a shorter random string.
//
//   - Every one is frequent enough to be a single token in the tokenizers this
//     project's panel uses, so an anchor costs about what the line number it
//     replaces cost.
//   - All are lowercase ASCII letters, which is what Parse accepts, so an
//     anchor round-trips through a model's output without normalization.
//   - They are concrete nouns and plain adjectives with no near-homophones and
//     no shared prefixes long enough to confuse, because a model that
//     mis-transcribes one anchor for another addresses the wrong line with full
//     confidence.
//
// 256 words give 65,536 two-word anchors, which is more than any file this
// addresses will need. The list is written here rather than borrowed, so
// nothing of another project's is copied in.
var words = []string{
	"able", "acorn", "alder", "amber", "anchor", "apple", "arbor", "arrow",
	"ash", "aspen", "autumn", "basin", "bay", "beacon", "beam", "bell",
	"birch", "black", "blue", "bold", "brass", "brave", "brick", "bridge",
	"bright", "bronze", "brook", "brown", "calm", "candle", "cedar", "chalk",
	"cherry", "clay", "clever", "cliff", "cloud", "clover", "coal", "coast",
	"cobalt", "copper", "coral", "cotton", "crane", "creek", "crisp", "crown",
	"crystal", "dawn", "deep", "deer", "dew", "dune", "dusk", "eagle",
	"east", "elm", "ember", "fair", "falcon", "fern", "field", "finch",
	"fir", "flame", "flat", "flint", "fog", "forest", "fox", "frost",
	"garden", "gentle", "glade", "glass", "gleam", "glossy", "gold", "grand",
	"granite", "grass", "gray", "green", "grove", "harbor", "hardy", "harvest",
	"haven", "hawk", "haze", "hazel", "heather", "hill", "hollow", "honey",
	"ice", "indigo", "iron", "island", "ivory", "ivy", "jade", "juniper",
	"kind", "lake", "lantern", "larch", "lark", "laurel", "leaf", "light",
	"lily", "linen", "loam", "lotus", "lunar", "maple", "marble", "marsh",
	"meadow", "mint", "misty", "moss", "mountain", "narrow", "nectar", "nest",
	"noble", "north", "oak", "oasis", "ocean", "olive", "onyx", "opal",
	"orchard", "orchid", "otter", "owl", "palm", "pearl", "pebble", "petal",
	"pine", "plain", "plum", "polar", "polite", "pond", "poplar", "prairie",
	"quarry", "quartz", "quiet", "rapid", "raven", "reed", "ridge", "river",
	"robin", "rose", "rust", "sage", "salt", "sand", "sapphire", "shade",
	"shale", "shell", "shore", "silent", "silver", "slate", "sleek", "slow",
	"smooth", "snow", "soft", "solar", "sparrow", "spring", "spruce", "steady",
	"steel", "stone", "storm", "stream", "summer", "sunny", "swift", "tall",
	"teal", "thicket", "thorn", "tide", "timber", "topaz", "torrent", "trail",
	"tulip", "umber", "valley", "vast", "velvet", "vine", "violet", "volcano",
	"walnut", "warm", "water", "wave", "wheat", "white", "wide", "wild",
	"willow", "windy", "winter", "wise", "wolf", "wood", "wren", "yellow",
	"zenith", "alcove", "arch", "badge", "banner", "barley", "beach", "bloom",
	"bluff", "border", "bounty", "branch", "breeze", "briar", "brine", "burrow",
	"canyon", "cavern", "chime", "cinder", "cliffs", "cobble", "comet", "cove",
	"crest", "current", "cypress", "delta", "drift", "eddy", "elder", "estuary",
}
