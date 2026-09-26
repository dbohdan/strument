package config

import (
	"strings"
	"testing"
)

// decision_model() is refused at load for anything that would otherwise fail
// at the first shell command, where the failure would only show as a prompt.
func TestDecisionModelRefusedAtLoad(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`approve_model = decision_model("systemone", "laya")`, "needs url="},
		{`approve_model = decision_model("systemone", "laya", url="")`, "needs url="},
		{`approve_model = decision_model("system1", "laya", url="http://localhost:11435/v1/systemone")`, "systemone"},
		{`approve_model = decision_model("systemone", "", url="http://localhost:11435/v1/systemone")`, "slug is empty"},
		{`approve_model = decision_model("systemone", "laya", url="localhost:11435")`, "scheme"},
		{`approve_model = decision_model("systemone", "laya", url="http:///v1/systemone")`, "no host"},
		{`approve_model = decision_model("systemone", "laya", url="http://h/x", threshold=0)`, "above 0"},
		{`approve_model = decision_model("systemone", "laya", url="http://h/x", threshold=1.5)`, "at most 1"},
		{`approve_model = decision_model("systemone", "laya", url="http://h/x", threshold="high")`, "number"},
		{`approve_model = decision_model("systemone", "laya", "http://h/x")`, "positional"},
		{`approve_model = decision_model("systemone", "laya", url="http://h/x", timeout=0)`, "timeout"},
		{`approve_model = decision_model("systemone", "laya", url="http://h/x", timeout=600)`, "at most 120"},
		{`approve_model = "laya"`, "decision_model() value"},
	} {
		_, err := loadBudget(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.src, err, tc.want)
		}
	}
}

// No vendor, gateway or URL is filled in, the key is kept, and the threshold
// defaults to the one the eval tested.
func TestDecisionModelParsed(t *testing.T) {
	cfg, err := loadBudget(t, `approve_model = decision_model("systemone", "laya", `+
		`url="http://localhost:11435/v1/systemone", api_key="local", proxy="direct")`)
	if err != nil {
		t.Fatal(err)
	}
	am := cfg.ApproveModel
	if am == nil {
		t.Fatal("approve_model was not parsed")
	}
	if am.Dialect != DecisionSystemOne || am.Slug != "laya" || am.URL != "http://localhost:11435/v1/systemone" ||
		am.APIKey != "local" || am.Proxy != "" || am.Threshold != DecisionDefaultThreshold ||
		am.Timeout != DecisionDefaultTimeout {
		t.Errorf("approve_model = %+v", *am)
	}
	if s := (&decisionModelValue{d: *am}).String(); strings.Contains(s, am.APIKey) {
		t.Errorf("the value's String shows the key: %s", s)
	}
}

// Unset is nil, and None is how a trusted project turns it off.
func TestDecisionModelOff(t *testing.T) {
	for _, src := range []string{"", "approve_model = None"} {
		cfg, err := loadBudget(t, src)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ApproveModel != nil {
			t.Errorf("%q: approve_model = %+v, want nil", src, *cfg.ApproveModel)
		}
	}
}
