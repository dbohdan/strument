package fixture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every llm.ContentBlock kind must be handled by every wire client.
//
// The bug this prevents is the one the image work was built on top of: the
// Anthropic adapter used to skip any block whose Text was empty, so a block of
// a new kind left the harness with no error, no log, and no request field. The
// model then answers plausibly about an image it never received, and the user
// reads that as the model being bad at vision rather than as a harness fault.
//
// Three dialects carry content today and they spell an image three different
// ways — Anthropic's {"type":"image","source":{...}}, chat-completions'
// {"type":"image_url","image_url":{"url":...}}, and the Responses API's
// {"type":"input_image","image_url":"..."} where image_url is a bare string.
// A kind added to one and forgotten in the others is the likely mistake, and
// it is invisible at runtime, so it is checked here at build time instead.
func TestEveryBlockKindIsHandledByEveryClient(t *testing.T) {
	root := repoRoot(t)

	types, err := os.ReadFile(filepath.Join(root, "internal", "llm", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	kindRE := regexp.MustCompile(`(?m)^\s*(Block[A-Za-z0-9_]+)\s*=\s*"`)
	var kinds []string
	for _, m := range kindRE.FindAllStringSubmatch(string(types), -1) {
		kinds = append(kinds, m[1])
	}
	// The counter-arm. If the constants are renamed or reshaped, the scan
	// above matches nothing and every assertion below passes by checking
	// nothing at all.
	if len(kinds) < 2 {
		t.Fatalf("found %d block kinds in internal/llm/types.go; the constant form must have changed, "+
			"and this check is passing because it found nothing to check", len(kinds))
	}

	clients := []string{"anthropic.go", "client.go", "responses.go"}
	for _, file := range clients {
		path := filepath.Join(root, "internal", "client", file)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(body)
		// Same counter-arm one level down: a client that stopped switching on
		// kinds entirely would otherwise report no missing kinds.
		if !strings.Contains(src, "llm.Block") {
			t.Errorf("internal/client/%s references no llm.Block kind at all; either it stopped "+
				"shaping content or this check no longer knows where to look", file)
			continue
		}
		for _, kind := range kinds {
			if !strings.Contains(src, "llm."+kind) {
				t.Errorf("internal/client/%s has no case for llm.%s; a block of that kind would "+
					"reach the provider mangled or not at all, with no error either way", file, kind)
			}
		}
	}
}
