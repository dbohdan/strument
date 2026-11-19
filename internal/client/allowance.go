package client

import (
	"context"
	"net/http"
	"strings"

	"dbohdan.com/strument/internal/allowance"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/httpx"
)

// GoAllowance returns a reader of p's opencode Go allowance, at
// <base_url>/usage, or nil when p is not an opencode provider. Zen, which
// prices per token and reports what it charged, shares the adapters; its
// endpoint answers the usage path with a refusal, which the coder takes as
// "no allowance here" and does not ask again.
func GoAllowance(p config.Provider) func(context.Context) (allowance.Reading, error) {
	url := goAllowanceURL(p)
	if url == "" {
		return nil
	}
	transport, err := httpx.ProxyTransport(p.Proxy)
	if err != nil || transport == nil {
		transport = httpx.QuickHandshake(nil)
	}
	hc := &http.Client{Transport: transport}
	return func(ctx context.Context) (allowance.Reading, error) {
		return allowance.Fetch(ctx, hc, url, p.APIKey, userAgent)
	}
}

// goAllowanceURL is where p's allowance would be, or "" for a provider that
// is not opencode's.
func goAllowanceURL(p config.Provider) string {
	switch p.Adapter {
	case config.AdapterOpenCode, config.AdapterOpenCodeAnthropic, config.AdapterOpenCodeResponses:
	default:
		return ""
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultOpenCodeBase
	}
	return base + "/usage"
}
