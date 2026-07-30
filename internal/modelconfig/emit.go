package modelconfig

import (
	"fmt"
	"strings"
)

// EmitStarlark renders one copy-pastable model() block. Objective fields become
// real values; the judgment calls (reasoning, reasoning_tag, weak_model) are
// emitted commented, so a pasted block reads as a scaffold to finish rather
// than a finished declaration. edit_format is deliberately omitted — the
// default "tool" fits the expected model class, and surfacing it would imply
// more variance than there is. The block ends with a trailing comma so it drops
// straight into a `models = {"alias": ...}` dict.
func EmitStarlark(info ModelInfo, providerName string) string {
	var b strings.Builder
	b.WriteString("model(\n")
	fmt.Fprintf(&b, "    %s,\n", providerName)
	fmt.Fprintf(&b, "    %q,\n", info.Slug)
	if info.DisplayName != "" {
		fmt.Fprintf(&b, "    display_name=%q,\n", info.DisplayName)
	}
	if info.Context > 0 {
		fmt.Fprintf(&b, "    context=%d,\n", info.Context)
	}
	if info.MaxOutput > 0 {
		fmt.Fprintf(&b, "    max_output=%d,\n", info.MaxOutput)
	}
	if info.InputCost != "" {
		fmt.Fprintf(&b, "    input_cost=%s,\n", info.InputCost)
	}
	if info.OutputCost != "" {
		fmt.Fprintf(&b, "    output_cost=%s,\n", info.OutputCost)
	}
	if info.CacheCapable {
		b.WriteString("    cache=True,  # OpenRouter reports prompt caching for this model.\n")
	}
	if info.Reasoning {
		b.WriteString("    # reasoning=\"low\",  # Uncomment and set the effort: \"low\", \"medium\", or \"high\".\n")
		b.WriteString("    # reasoning_tag=\"think\",  # Uncomment if the model emits reasoning in inline tags.\n")
	}
	b.WriteString("    # weak_model=\"...\",  # Uncomment to use a cheaper model for summaries and commits.\n")
	b.WriteString("),\n")
	return b.String()
}
