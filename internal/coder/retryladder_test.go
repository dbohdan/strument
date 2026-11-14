package coder

import (
	"context"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// The ladder retry_timeout configures. The default has to stay aider's to the
// retry — eight waits, 0.25s doubling to 32s, then give up — because moving the
// cap from the next delay onto the total waited is only meant to change what a
// raised budget does. A raised one keeps retrying at maxRetryDelay until the
// waits reach it, rather than doubling into a single sleep of minutes.
func TestRetryLadder(t *testing.T) {
	ladder := func(budget time.Duration) []time.Duration {
		rb := retryBackoff{delay: initialRetryDelay, cap: budget}
		clock := &fastClock{}
		transient := &llm.StreamError{Class: llm.ErrRateLimit, Message: "429"}
		for rb.retry(context.Background(), &summaryOutput{}, clock, transient) {
			if len(clock.slept) > 100 {
				t.Fatal("the ladder does not terminate")
			}
		}
		return clock.slept
	}
	sum := func(ds []time.Duration) (total time.Duration) {
		for _, d := range ds {
			total += d
		}
		return total
	}

	def := ladder(0)
	if len(def) != 8 || def[len(def)-1] != 32*time.Second {
		t.Errorf("default ladder = %v, want aider's eight waits ending at 32s", def)
	}

	long := ladder(10 * time.Minute)
	for _, d := range long {
		if d > maxRetryDelay {
			t.Errorf("a wait of %v exceeds maxRetryDelay", d)
		}
	}
	if total := sum(long); total < 10*time.Minute || total >= 10*time.Minute+maxRetryDelay {
		t.Errorf("a 10m budget waited %v in total, want at least 10m and less than one more wait past it", total)
	}
}
