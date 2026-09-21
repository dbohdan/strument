package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/history"
)

// usageCmd reports per-provider usage and cost, from the per-provider ledgers
// under $XDG_STATE_HOME/strument/usage/.
//
// The ledgers are global, so this command answers "what is this provider
// costing me" across every project, which is the question the per-project
// cost ledger cannot: `cat projects/*/cost.jsonl` aggregates, but it cannot
// split by provider without re-parsing every row by hand.
//
// The argument is a provider name, `all` for every provider, or empty for the
// default model's provider. The three windows are rolling — last 24 hours, 7
// days, 30 days — and the report says so, because a total labeled "month"
// beside a provider's calendar invoice would read as a discrepancy rather
// than as a different question.
type usageCmd struct {
	Provider string `arg:""                                     help:"Provider name, or 'all' for every provider (default: the default model's)." optional:""`
}

func (c *usageCmd) Run() error {
	provider := strings.TrimSpace(c.Provider)
	if provider == history.UsageAll {
		return runUsageAll()
	}
	if provider == "" {
		cfg, err := loadProjectConfig()
		if err != nil {
			return err
		}
		provider = defaultProvider(cfg)
		if provider == "" {
			return fmt.Errorf("no provider to report: the config names no default model")
		}
	}
	return runUsageOne(provider)
}

// runUsageOne prints one provider's three windows, and refuses a provider
// with nothing recorded — the way an unknown model alias is answered with the
// aliases that exist, because "no such thing" with no list attached sends the
// user hunting through their config.
func runUsageOne(provider string) error {
	known, err := history.UsageProviders()
	if err != nil {
		return err
	}
	if !slices.Contains(known, provider) {
		if len(known) == 0 {
			return fmt.Errorf("no usage recorded yet for any provider; usage tracking begins when the version that writes it runs")
		}
		return fmt.Errorf("no usage recorded for provider %q (with usage: %s)", provider, strings.Join(known, ", "))
	}
	rows, err := history.ReadUsage(provider)
	if err != nil {
		return err
	}
	fmt.Print(history.FormatUsage(provider, rows, timeNow()))
	return nil
}

// runUsageAll prints every provider that has a ledger, one after another, so
// the total question is one command rather than one invocation per provider.
func runUsageAll() error {
	known, err := history.UsageProviders()
	if err != nil {
		return err
	}
	if len(known) == 0 {
		return fmt.Errorf("no usage recorded yet for any provider; usage tracking begins when the version that writes it runs")
	}
	for _, provider := range known {
		rows, err := history.ReadUsage(provider)
		if err != nil {
			return err
		}
		fmt.Print(history.FormatUsage(provider, rows, timeNow()))
	}
	return nil
}

// defaultProvider resolves the provider an empty argument means: the default
// model's, which is what "my usage" means before anyone says otherwise.
func defaultProvider(cfg *config.Config) string {
	if m, ok := cfg.Models[cfg.Default]; ok {
		return config.ProviderName(m)
	}
	return ""
}

// timeNow is the clock seam for the report's "as of" stamp and windows.
var timeNow = time.Now
