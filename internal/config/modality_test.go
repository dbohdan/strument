package config

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// The send-path projection asks the model whether it accepts a block's own
// Type, so a modality name that is not a block kind is a declaration nothing
// reads. Holding the two lists together here means a new kind cannot be added
// on one side alone.
func TestModalityNamesMatchBlockKinds(t *testing.T) {
	for _, kind := range []string{llm.BlockText, llm.BlockImage} {
		if !knownModalities[kind] {
			t.Errorf("block kind %q is not a known modality; input_modalities could never enable it", kind)
		}
	}
	if len(knownModalities) < 2 {
		t.Fatalf("only %d known modalities; this check has nothing to check", len(knownModalities))
	}
}

// Text is unconditional: a config naming only "image" must still produce a
// model you can say something to.
func TestAcceptsTextAlways(t *testing.T) {
	for name, m := range map[string]*Model{
		"nothing declared": {},
		"image only":       {InputModalities: []string{llm.BlockImage}},
	} {
		if !m.Accepts(llm.BlockText) {
			t.Errorf("%s: model refuses text", name)
		}
	}
}

func TestAcceptsImageOnlyWhenDeclared(t *testing.T) {
	if (&Model{}).Accepts(llm.BlockImage) {
		t.Error("a model with nothing declared claimed to accept images; the safe default is text only")
	}
	if !(&Model{InputModalities: []string{llm.BlockText, llm.BlockImage}}).Accepts(llm.BlockImage) {
		t.Error("a model declaring images refused one")
	}
}

// A typo must not read as "text only" with nothing to explain it. The user
// would see a model that cannot see images and no reason anywhere.
func TestUnknownModalityIsRejectedAtLoad(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "vendor/slug", input_modalities = ["text", "images"])}
default = "m"
`
	_, err := Load(harness(t, src, "", testEnv))
	if err == nil {
		t.Fatal("a misspelled modality loaded without complaint")
	}
	if !strings.Contains(err.Error(), "images") || !strings.Contains(err.Error(), "unknown input modality") {
		t.Errorf("the error does not name the offending value: %v", err)
	}
}

// The happy path, and the default beside it.
func TestInputModalitiesParsed(t *testing.T) {
	src := `
p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {
    "seeing": model(p, "vendor/vision", input_modalities = ["text", "image"]),
    "plain": model(p, "vendor/text"),
}
default = "seeing"
`
	cfg, err := Load(harness(t, src, "", testEnv))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Models["seeing"].Accepts(llm.BlockImage) {
		t.Error("a declared image modality did not reach the model")
	}
	if cfg.Models["plain"].Accepts(llm.BlockImage) {
		t.Error("an undeclared model accepts images; the default must be text only")
	}
}
