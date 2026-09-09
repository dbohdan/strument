package modelconfig

import (
	"fmt"
	"slices"
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
// reasoning_tag, side_model) are emitted commented, so a pasted entry reads as a
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

var defaultReasoningEfforts = []string{"max", "xhigh", "high", "medium", "low", "minimal"}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf("%q", value)
	}
	return strings.Join(quoted, ", ")
}

func reasoningEfforts(info ModelInfo) []string {
	efforts := info.ReasoningEfforts
	if info.ReasoningMetadata {
		switch {
		case info.ReasoningEffortsAny:
			efforts = defaultReasoningEfforts
		case info.ReasoningEffortsKnown:
			// Use the catalog's exact list, including an empty list.
		case len(efforts) > 0:
			// A manually constructed ModelInfo can carry the list without the
			// source parser's presence marker.
		default:
			return nil
		}
	}
	if len(efforts) == 0 && !info.ReasoningMetadata {
		efforts = []string{"low", "medium", "high"}
	}
	out := make([]string, 0, len(efforts)+1)
	for _, effort := range efforts {
		if effort == "none" || effort == "off" {
			continue
		}
		if effort == "" || slices.Contains(out, effort) {
			continue
		}
		out = append(out, effort)
	}
	return out
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
		efforts := reasoningEfforts(info)
		defaultEffort := info.ReasoningDefault
		if !info.ReasoningMandatory && info.ReasoningMetadata && info.ReasoningDefaultEnabled != nil && !*info.ReasoningDefaultEnabled {
			defaultEffort = "off"
		} else if !info.ReasoningMandatory && defaultEffort == "none" {
			defaultEffort = "off"
		}
		if defaultEffort == "" {
			if info.ReasoningMetadata {
				defaultEffort = "default"
			} else {
				defaultEffort = "low"
				if !slices.Contains(efforts, defaultEffort) {
					defaultEffort = efforts[len(efforts)-1]
				}
			}
		}
		effortComment := "the provider default; OpenRouter reports no selectable efforts"
		if len(efforts) > 0 {
			effortComment = quotedList(efforts)
		}
		if info.ReasoningMetadata && !info.ReasoningMandatory {
			effortComment += ", or \"off\""
		}
		fmt.Fprintf(&b, "        # reasoning=%q,  # Uncomment and set the effort: %s.\n", defaultEffort, effortComment)
		b.WriteString("        # reasoning_tag=\"think\",  # Uncomment if the model emits reasoning in inline tags.\n")
	}
	b.WriteString("        # side_model=\"...\",  # Uncomment to use a different model for summaries and commits.\n")
	b.WriteString("    ),\n")
	return b.String()
}
