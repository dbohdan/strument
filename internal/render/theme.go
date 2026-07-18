package render

// Theme is the REPL's color palette, mirroring aider's scheme so a
// returning aider user feels no seam (args.py + the --dark-mode/--light-mode
// blocks in main.py). Each field is an SGR parameter string (no CSI/"m"
// wrapper); the render and repl packages share one Theme so their colors
// never drift. An empty field means "terminal default".
type Theme struct {
	UserInput string // prompt and horizontal rules
	Assistant string // base color for all assistant (markdown) output
	Error     string // tool errors
	Warning   string // tool warnings
	Code      string // color for inline code and code blocks (defaults to white / "37")
}

// DefaultTheme is aider's default palette: green input, blue assistant,
// bright-red errors, orange warnings. Truecolor for the exact hex values.
func DefaultTheme() Theme {
	return Theme{
		UserInput: "38;2;0;204;0",   // #00cc00
		Assistant: "38;2;0;136;255", // #0088ff
		Error:     "38;2;255;34;34", // #FF2222
		Warning:   "38;2;255;165;0", // #FFA500
		Code:      "37",             // white
	}
}

// DarkTheme is aider's --dark-mode palette (brighter, for dark terminals).
func DarkTheme() Theme {
	return Theme{
		UserInput: "38;2;50;255;50", // #32FF32
		Assistant: "38;2;0;255;255", // #00FFFF
		Error:     "38;2;255;51;51", // #FF3333
		Warning:   "38;2;255;255;0", // #FFFF00
		Code:      "37",
	}
}

// LightTheme is aider's --light-mode palette. aider names these colors
// ("green"/"blue"/"red"), which map to the 16-color SGRs; the warning stays
// truecolor orange.
func LightTheme() Theme {
	return Theme{
		UserInput: "32",             // green
		Assistant: "34",             // blue
		Error:     "31",             // red
		Warning:   "38;2;255;165;0", // #FFA500
		Code:      "37",
	}
}
