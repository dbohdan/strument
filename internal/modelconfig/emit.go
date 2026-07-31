package modelconfig

import (
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/config"
)

// EmitStarlark renders the models as a copy-pastable `models = {...}` dict, each
// entry keyed "<alias>": model(...). The alias is the slug's core
// (config.SlugCore, the same reduction a missing display_name falls back to),
// deduped with a numeric suffix so two slugs that reduce alike never collide
// into one key and silently drop a model — rename it to the short name you'll
// type. It is a from-scratch scaffold: to add to a config that already defines
// `models`, splice the two dicts rather than pasting a second `models =`.
//
// Objective fields become real values; the judgment calls (reasoning,
// reasoning_tag, weak_model) are emitted commented, so a pasted entry reads as a
// scaffold to finish rather than a finished declaration. edit_format is
// deliberately omitted — the default "tool" fits the expected model class.
func EmitStarlark(infos []ModelInfo, providerName string) string {
	used := make(map[string]bool, len(infos))
	var b strings.Builder
	b.WriteString("models = {\n")
	for _, info := range infos {
		b.WriteString(emitEntry(info, providerName, uniqueAlias(config.SlugCore(info.Slug), used)))
	}
	b.WriteString("}\n")
	return b.String()
}

// uniqueAlias returns base, or base-2, base-3, … — the first form not already
// in used — and records it.
func uniqueAlias(base string, used map[string]bool) string {
	alias := base
	for n := 2; used[alias]; n++ {
		alias = fmt.Sprintf("%s-%d", base, n)
	}
	used[alias] = true
	return alias
}

func emitEntry(info ModelInfo, providerName, alias string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "    %q: model(\n", alias)
	fmt.Fprintf(&b, "        %s,\n", providerName)
	fmt.Fprintf(&b, "        %q,\n", info.Slug)
	if info.DisplayName != "" {
		fmt.Fprintf(&b, "        display_name=%q,\n", info.DisplayName)
	}
	if info.Context > 0 {
		fmt.Fprintf(&b, "        context=%d,\n", info.Context)
	}
	if info.MaxOutput > 0 {
		fmt.Fprintf(&b, "        max_output=%d,\n", info.MaxOutput)
	}
	if info.InputCost != "" {
		fmt.Fprintf(&b, "        input_cost=%s,\n", info.InputCost)
	}
	if info.OutputCost != "" {
		fmt.Fprintf(&b, "        output_cost=%s,\n", info.OutputCost)
	}
	if info.CacheCapable {
		b.WriteString("        cache=True,  # OpenRouter reports prompt caching for this model.\n")
	}
	if info.Reasoning {
		b.WriteString("        # reasoning=\"low\",  # Uncomment and set the effort: \"low\", \"medium\", or \"high\".\n")
		b.WriteString("        # reasoning_tag=\"think\",  # Uncomment if the model emits reasoning in inline tags.\n")
	}
	b.WriteString("        # weak_model=\"...\",  # Uncomment to use a cheaper model for summaries and commits.\n")
	b.WriteString("    ),\n")
	return b.String()
}
