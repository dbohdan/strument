package coder

import (
	"regexp"
	"sync"
)

// reasoningTagRe compiles `(?s)<tag>.*?</tag>` with the tag quoted
// (basecoder-spec §5: DOTALL, regexp.QuoteMeta). Cached per tag.
var (
	reasoningReMu    sync.Mutex
	reasoningReCache = map[string]*regexp.Regexp{}
)

func reasoningTagRe(tag string) *regexp.Regexp {
	reasoningReMu.Lock()
	defer reasoningReMu.Unlock()
	if re, ok := reasoningReCache[tag]; ok {
		return re
	}
	re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(tag) + `>.*?</` + regexp.QuoteMeta(tag) + `>`)
	reasoningReCache[tag] = re
	return re
}
