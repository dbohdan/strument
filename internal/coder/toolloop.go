package coder

import "strconv"

// Tool-call loop detection: the sibling of loopdetect.go's text detector,
// watching the other stream a stuck turn produces.
//
// loopdetect.go watches streamed *text* for repeating windows, and it was
// tuned on a corpus of real loops — but a loop can run without ever repeating
// a sentence. The live episode that motivated this one orbited a file: the
// same read call with the same arguments, fifteen times, between prose that
// said "making the edits now" and edit calls that never came. Every streamed
// line was fresh; every tool call but the reads was absent. The step budget
// caught it at 50 steps, which is late, and its question to the user carried
// the diagnosis ("edited 0 files") that the model never saw.
//
// The detection here is the smallest one that sees that episode, and it is
// deliberately not a heuristic: the key is the tool name plus its exact
// arguments, and the rule is counting.
//
// The first detector is the exact repeat: three identical read-class calls
// since the last mutation. The second counts read-class calls since the last
// mutation, not since the turn began. That distinction is the salvage of a
// dead idea: a raw per-turn total cannot tell the hardest working turns —
// dozens of reads, interleaved with edits — from a loop, because the count is
// the same in both. What separates them is whether anything ever changed. Hard
// work alternates read/read/edit/read/edit, and every edit is evidence of
// progress; a loop reads and reads with the world never moving. The tools
// that mutate are therefore the reset signal, and they are the same list the
// counting exempts — one list, two uses: exempt from counting, and proof of
// movement when it happens.
//
// edit and write self-limit besides: a repeated identical edit fails loudly
// on old_string no longer matching, because the old text is already gone.
const (
	// toolLoopMaxIdentical is how many times one read-class call may repeat
	// exactly — same tool, same arguments — since the last mutation, before
	// the next send carries a note saying so. Three: once is work, twice can
	// be legitimate (re-read after an edit, re-grep after a rename), three
	// identical reads with nothing having changed in between is the orbit the
	// episode showed.
	toolLoopMaxIdentical = 3
	// toolLoopMaxReads is the since-last-mutation cap that catches the loop
	// that varies its arguments — the "read lines 1-10, 2-11, 3-12" slide —
	// where no single call repeats but the reads still pile up over a world
	// that never changes. High enough that a hard working turn, whose streaks
	// between edits are short, never meets it.
	toolLoopMaxReads = 20
)

// toolLoopWatcher counts tool calls within one turn, per the two detectors
// above.
type toolLoopWatcher struct {
	counts map[string]int
	// reads is the count since the last mutation.
	reads int
	// fired notes once: the reflection is injected once per turn, on the send
	// after a threshold trips, and repeating it every step would itself be
	// noise.
	fired bool
}

func newToolLoopWatcher() *toolLoopWatcher {
	return &toolLoopWatcher{counts: map[string]int{}}
}

// exempt tools self-limit or legitimately repeat: a repeated edit fails on
// old_string no longer matching, and bash/check/commit are the tools a
// productive turn calls again and again with unchanged arguments. They are
// also the mutation signal: see observeMutation.
func toolLoopExempt(name string) bool {
	switch name {
	case toolBash, toolCheck, toolCommit, toolEdit, toolWrite:
		return true
	}
	return false
}

// observeCall records a dispatched call and returns the loop note to inject
// into the next request, or "". The arguments are the raw JSON as the model
// sent them — two calls that differ only in whitespace are the same call to
// a loop, and normalizing beyond whitespace is the overeager part this file
// declined to do.
func (w *toolLoopWatcher) observeCall(name, argsJSON string) string {
	if w == nil || toolLoopExempt(name) {
		return ""
	}
	if w.fired {
		return "" // said once per turn; the step budget is the backstop
	}
	if name == toolInterrupt {
		return "" // ending the turn is the loop's exit, not part of it
	}
	key := name + "\x00" + argsJSON
	w.counts[key]++
	w.reads++
	if w.counts[key] >= toolLoopMaxIdentical {
		w.fired = true
		return "You have made the same " + name + " call with the same arguments " +
			"several times since anything last changed. The earlier results are " +
			"already in the conversation above. If you are stuck in a loop, take " +
			"a different approach — and if there is nothing useful left to do, " +
			"call the interrupt tool to end the turn."
	}
	if w.reads >= toolLoopMaxReads {
		w.fired = true
		return "This turn has made " + strconv.Itoa(w.reads) + " read-only tool calls since " +
			"anything last changed. If you are gathering more than that without " +
			"acting on what you have, take a different approach — and if there is " +
			"nothing useful left to do, call the interrupt tool to end the turn."
	}
	return ""
}

// observeMutation records that the world changed: the since-last-mutation
// streak resets, including the exact-repeat counts, because a re-read after
// an edit is a fresh question about a fresh file. Called for the exempt
// tools, which are the progress signal — the same list that exempts them from
// counting is what proves movement when it happens.
func (w *toolLoopWatcher) observeMutation() {
	if w == nil {
		return
	}
	w.reads = 0
	w.counts = map[string]int{}
}
