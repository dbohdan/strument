package config

import (
	"strings"
	"testing"
)

// Headers on the provider apply to every model on it; a model's own replace
// them by name, whatever the case, so a header is sent once.
func TestHeadersMergeModelOverProvider(t *testing.T) {
	src := `
p = provider("openai", api_key = "k", headers = {"x-cmd-zdr": "1", "X-Team": "core"})
models = {
    "a": model(p, "s"),
    "b": model(p, "s", headers = {"X-CMD-ZDR": "0", "X-Extra": "yes"}),
}
default = "a"
`
	cfg, err := Load(harness(t, src, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	a, b := cfg.Models["a"].RequestHeaders(), cfg.Models["b"].RequestHeaders()
	if a["X-Cmd-Zdr"] != "1" || a["X-Team"] != "core" || len(a) != 2 {
		t.Errorf("a = %v, want the provider's two headers", a)
	}
	if b["X-Cmd-Zdr"] != "0" || b["X-Team"] != "core" || b["X-Extra"] != "yes" || len(b) != 3 {
		t.Errorf("b = %v, want the model's value for X-Cmd-Zdr and one copy of it", b)
	}
	if h := (&Model{}).RequestHeaders(); h != nil {
		t.Errorf("no headers = %v, want nil", h)
	}
}

// What a config may not set: the headers Strument owns, names that are not
// HTTP tokens, values with line breaks, and anything that is not a string.
func TestHeadersRefused(t *testing.T) {
	for _, tc := range []struct{ headers, want string }{
		{`{"Authorization": "Bearer x"}`, "set `api_key` instead"},
		{`{"x-api-key": "x"}`, "set `api_key` instead"},
		{`{"content-type": "text/plain"}`, "cannot be replaced"},
		{`{"Bad Name": "x"}`, "not a valid header name"},
		{`{"X-Ok": "a\r\nX-Evil: 1"}`, "line break"},
		{`{"X-Ok": 1}`, "not a string"},
		{`{1: "x"}`, "not a string"},
	} {
		for _, where := range []string{"provider", "model"} {
			src := `
p = provider("openai", api_key = "k"` + map[bool]string{true: ", headers = " + tc.headers, false: ""}[where == "provider"] + `)
models = {"m": model(p, "s"` + map[bool]string{true: ", headers = " + tc.headers, false: ""}[where == "model"] + `)}
default = "m"
`
			_, err := Load(harness(t, src, "", nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s %s: err = %v, want %q", where, tc.headers, err, tc.want)
			}
		}
	}
}

// decision_model() takes headers under the same rules.
func TestDecisionModelHeaders(t *testing.T) {
	base := `
p = provider("openai", api_key = "k")
models = {"m": model(p, "s")}
default = "m"
`
	cfg, err := Load(harness(t, base+`approve_model = decision_model("systemone", "laya", url = "https://x.example/v1/systemone", headers = {"x-cmd-zdr": "1"})`+"\n", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ApproveModel == nil || cfg.ApproveModel.Headers["x-cmd-zdr"] != "1" {
		t.Errorf("approve_model headers = %+v", cfg.ApproveModel)
	}
	_, err = Load(harness(t, base+`approve_model = decision_model("systemone", "laya", url = "https://x.example/v1/systemone", headers = {"Authorization": "x"})`+"\n", "", nil))
	if err == nil || !strings.Contains(err.Error(), "set `api_key` instead") {
		t.Errorf("err = %v, want the reserved header refused", err)
	}
}
