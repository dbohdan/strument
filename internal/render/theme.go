package render

// Theme is the REPL's color palette, mirroring aider's scheme so a
// returning aider user feels no seam (args.py + the --dark-mode/--light-mode
// blocks in main.py). Each field is an SGR parameter string (no CSI/"m"
// wrapper); the render and repl packages share one Theme so their colors
// never drift. An empty field means "terminal default".
type Theme struct {
	UserInput   string // prompt and horizontal rules
	Assistant   string // base color for all assistant (markdown) output
	Error       string // tool errors
	Warning     string // tool warnings
	Code        string // color for inline code and code blocks (defaults to white / "37")
	Link        string // markdown links (underline + color)
	DiffRemoved string // removed ("-") lines in a tool-call diff
	DiffAdded   string // added ("+") lines in a tool-call diff
	Command     string // suggested-command ("$") lines
	Tool        string // the harness reporting what a tool did

	// Reasoning is how the model's thinking recedes against the answer. It is
	// SGR 2 (faint) rather than a color, and that is deliberate: faint is
	// relative to whatever foreground the user's theme sets, while a fixed color
	// is a bet on their palette.
	//
	// The bet was lost once already. This was "90" — bright black, palette slot
	// 8 — which Solarized repurposes as base03, the background color of
	// Solarized dark. On a canonical Solarized terminal the thinking rendered as
	// nothing at all. Faint has the opposite failure: a terminal that does not
	// implement it (QTerminal) shows ordinary readable text. One fails safe, the
	// other fails invisible, which is the whole argument.
	//
	// "2;"+Assistant is the variant to try if faint-but-in-palette-hue reads
	// better than faint-on-default.
	Reasoning string
}

// DefaultTheme is aider's default palette: green input, blue assistant,
// bright-red errors, orange warnings. Truecolor for the exact hex values.
func DefaultTheme() Theme {
	return Theme{
		UserInput:   "38;2;0;204;0",   // #00cc00
		Assistant:   "38;2;0;136;255", // #0088ff
		Error:       "38;2;255;34;34", // #FF2222
		Warning:     "38;2;255;165;0", // #FFA500
		Code:        "37",             // white
		Link:        "4;34",           // underline + blue
		DiffRemoved: "31",             // red
		DiffAdded:   "32",             // green
		Command:     "36",             // cyan
		Tool:        "36",             // cyan
		Reasoning:   "2",              // faint; see the field comment before changing
	}
}

// DarkTheme is aider's --dark-mode palette (brighter, for dark terminals).
func DarkTheme() Theme {
	return Theme{
		UserInput:   "38;2;50;255;50", // #32FF32
		Assistant:   "38;2;0;255;255", // #00FFFF
		Error:       "38;2;255;51;51", // #FF3333
		Warning:     "38;2;255;255;0", // #FFFF00
		Code:        "37",
		Link:        "4;34", // diff/link colors match across modes (git convention)
		DiffRemoved: "31",
		DiffAdded:   "32",
		Command:     "36",
		Tool:        "36",
		Reasoning:   "2",
	}
}

// LightTheme is aider's --light-mode palette. aider names these colors
// ("green"/"blue"/"red"), which map to the 16-color SGRs; the warning stays
// truecolor orange.
func LightTheme() Theme {
	return Theme{
		UserInput:   "32",             // green
		Assistant:   "34",             // blue
		Error:       "31",             // red
		Warning:     "38;2;255;165;0", // #FFA500
		Code:        "37",
		Link:        "4;34",
		DiffRemoved: "31",
		DiffAdded:   "32",
		Command:     "36",
		Tool:        "36",
		Reasoning:   "2",
	}
}
