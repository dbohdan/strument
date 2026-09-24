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
	// fired notes once: the reflection is injected once per streak, on the
	// send after a threshold trips, and repeating it every step would itself
	// be noise.
	fired bool
	// stop is set when a streak trips its threshold a second time — the same
	// call three more times after the note, or as many reads again — and asks
	// the dispatcher to end the turn. The note used to be the last word, with
	// the step budget as the backstop, and --yes steps removes the budget: in
	// the run_code arms trial a model that had been told went on making the
	// same read for ten minutes. A loop the model keeps up after being told is
	// not work in progress, attended or not.
	stop bool
}

func newToolLoopWatcher() *toolLoopWatcher {
	return &toolLoopWatcher{counts: map[string]int{}}
}

// exempt tools legitimately repeat with unchanged arguments: bash, check and
// commit are what a productive turn calls again and again.
//
// edit and write used to be here too, on the reasoning that a repeated edit
// self-limits because old_string no longer matches once the first one lands.
// That holds for an edit that *succeeds* and is exactly wrong for one that
// fails: an ambiguous or unmatched edit changes nothing, so resending it
// verbatim fails identically forever. A field report has GLM-5.3-Flash doing
// precisely that -- the same byte-identical ambiguous edit, immediately after
// being told it was ambiguous -- with nothing to notice it, because the tool
// that could was excused from looking.
func toolLoopExempt(name string) bool {
	switch name {
	case toolBash, toolCheck, toolCommit:
		return true
	}
	return false
}

// countsAsRead separates the two detectors. A failed edit is worth counting as
// a repeat, but it is not an observation, so it must not push the read cap or
// the note would tell a model it had made read-only calls it did not make.
func countsAsRead(name string) bool {
	return name != toolEdit && name != toolWrite
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
	if name == toolInterrupt {
		return "" // ending the turn is the loop's exit, not part of it
	}
	key := name + "\x00" + argsJSON
	w.counts[key]++
	if countsAsRead(name) {
		w.reads++
	}
	if w.fired {
		// Said once; counting goes on, so the streak that earned the note can
		// earn the stop.
		if w.counts[key] >= 2*toolLoopMaxIdentical || w.reads >= 2*toolLoopMaxReads {
			w.stop = true
		}
		return ""
	}
	if w.counts[key] >= toolLoopMaxIdentical {
		w.fired = true
		return "You have made the same " + name + " call with the same arguments " +
			"several times since anything last changed. The earlier results are " +
			"already in the conversation above — including why the call did not " +
			"do what you wanted, if it failed. Resending it unchanged will fail " +
			"the same way. Take a different approach, and if there is nothing " +
			"useful left to do, call the interrupt tool to end the turn."
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
	// A new streak earns its own note: the loop that follows progress is not
	// the one the earlier note was about.
	w.fired = false
	w.stop = false
	w.reads = 0
	w.counts = map[string]int{}
}

// takeStop reports whether the turn should end on a loop, and starts the
// watcher over: if the user says to try again, the next streak is judged on
// its own, note first.
func (w *toolLoopWatcher) takeStop() bool {
	if w == nil || !w.stop {
		return false
	}
	*w = *newToolLoopWatcher()
	return true
}
