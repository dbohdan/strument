package client

import (
	"testing"

	"dbohdan.com/strument/internal/config"
)

func TestGoAllowanceURL(t *testing.T) {
	for _, tc := range []struct {
		p    config.Provider
		want string
	}{
		{config.Provider{Adapter: config.AdapterOpenCode}, "https://opencode.ai/zen/go/v1/usage"},
		{config.Provider{Adapter: config.AdapterOpenCodeAnthropic}, "https://opencode.ai/zen/go/v1/usage"},
		{config.Provider{Adapter: config.AdapterOpenCodeResponses, BaseURL: "https://opencode.ai/zen/v1/"}, "https://opencode.ai/zen/v1/usage"},
		{config.Provider{Adapter: config.AdapterOpenRouter}, ""},
		{config.Provider{Adapter: config.AdapterOpenAI, BaseURL: "https://opencode.ai/zen/go/v1"}, ""},
	} {
		if got := goAllowanceURL(tc.p); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.p, got, tc.want)
		}
		if (GoAllowance(tc.p) == nil) != (tc.want == "") {
			t.Errorf("%+v: reader presence does not match the URL", tc.p)
		}
	}
}
