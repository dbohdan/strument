# Anchored edits, phase 1: arm D wins its metric and loses the trial

Pre-registered in
[`2026-09-anchored-edit-preregistration.md`](2026-09-anchored-edit-preregistration.md);
[phase 0](2026-09-anchored-edit-phase0.md), [M9](2026-09-anchored-edit-m9.md)
and [M1](2026-09-anchored-edit-m1.md) precede it.

Arm A (today's `edit`) against arm D (anchored edits, as built in
`internal/anchor` and `internal/coder/anchors.go`). 6 models × 3 fixtures × 4
reps × 2 arms = **144 runs**, shuffled, 4 concurrent. **$0.4884.** Data:
[`phase1-results.jsonl`](2026-09-anchored-edit-data/phase1-results.jsonl).

## Result

| | arm A | arm D |
| --- | --- | --- |
| **task correct** | **72/72** | **64/72** |
| first-try edit success | 83.2% | **100.0%** |
| edit calls / failures | 119 / 20 | 182 / **0** |
| files that parse | 72/72 | **70/72** |
| gofmt-clean output | 72/72 | **42/72** |
| anchor text written into source | 0 | 2 |
| input tokens per run | 36,332 | **78,865** |
| output tokens per run | 645 | **1,610** |
| steps per run | 4.58 | 5.57 |
| cost | $0.188 | $0.301 |

**Arm D does exactly what it was built to do.** Ambiguity is gone — not reduced,
gone. 182 edit calls, zero failures, against arm A's 20 failures in 119. On
`dense`, the ambiguity fixture, arm A failed 17 edit calls and arm D failed none.
The mechanism works as designed: an anchor names one line, so "the text appears
three times" cannot be said.

**And it loses the trial anyway**, because it converts a loud recoverable failure
into a silent wrong one.

## What goes wrong

**Indentation, in 30 of 72 outputs.** Under `old_string` the model quotes text
back and the line matcher repairs whitespace drift — M9 measured that tier
rescuing 8 of luna's 12 edits. Anchored editing has no matching at all: the
anchor names the line, and whatever `new_string` says is written verbatim. The
repair mechanism is gone by construction, and the error it was repairing is not.

```
arm A   func RegisterMetric42(reg *Registry) error {
        →   if err := reg.Add("metric_42", HighResBuckets); err != nil {

arm D   func RegisterMetric42(reg *Registry) error {
        →       if err := reg.Add("metric_42", HighResBuckets); err != nil {
```

Semantically right, one tab too deep. Go does not care; gofmt and the diff the
user reads do.

**The anchor itself, written into the file, twice.** The row format is
`anchor<TAB>line`, and two models echoed the whole row back as replacement
content:

```
grove-harbor	var DefaultBuckets = []float64{0.1, 0.5, 1, 5}
cherry-elder			http.Error(w, "archived", http.StatusGone)
```

The first produced `expected declaration, found grove`. A leading line number is
obviously not code; a leading lowercase word is not obviously anything, and two
of six models put it in the file. Arm A cannot make this mistake — there is no
address in its rows to copy.

Strument's parse check caught one of them and said so. `tencent/hy3` read the
warning and reasoned it away: *"The parser warning was just because I added a
line making the line count change; it's fine now."* Then finished. A harness
warning is not a safety net if the thing being warned is also the thing deciding
whether the warning matters.

## The token cost was not the format's overhead

Phase 0 measured the anchored row at **+4.2%** input over the numbered format
and I carried that number forward as arm D's cost. Measured live, arm D used
**2.2× the input** and **2.5× the output**.

The format costs 4.2%. The *behaviour it induces* costs the rest: 5.57 steps a
run against 4.58, and 2.53 edit calls against 1.65, because replacing whole
lines takes more calls than replacing a span, and because a model that has just
been handed fresh anchors reads again anyway.

**A format's cost is not its token overhead. It is what it makes the model do.**
Phase 0 measured the first carefully and I treated it as the second — the same
error in kind as quoting a 61% output saving without an input column, which is
what phase 0 was written to catch.

## The loop closes on the indent column

M9 dropped arm C — yoneda's indent column — because it cannot be argued for on
tokens: any run of whitespace is already one token, so naming it in words costs
more than it saves.

Phase 1 shows what the column is actually for. **It exists because anchored
editing removes the whitespace safety net.** In yoneda the model never types
indentation; it names it, and `parseIndent` validates the name. That is not a
token optimization at all — it is the replacement for the fuzzy tier that
anchoring takes away, and arm D was built without it precisely because M9 had
judged it on the wrong axis.

That is a hypothesis for a further arm, not a conclusion. It predicts that
anchors plus the indent column would keep the 100% first-try rate and recover
the formatting, at +17.7% input on top of arm D's already-doubled traffic. It
would have to beat arm A's 72/72 to be worth anything, and arm A is hard to beat.

## Verdict

`anchored_edits` stays **off by default**. The feature is built, tested and
documented; this is the measurement that says not to turn it on.

The honest summary is that anchoring solved the problem it was aimed at and the
problem was not the one that mattered. Arm A's 20 edit failures cost round trips
and the model recovered from every one — 72/72 correct. Arm D's zero failures
cost eight wrong outcomes, thirty misformatted files and two that would not
compile, none of which the model noticed.

## What this does not settle

Three fixtures, one language, 72 runs an arm. Go's `if err != nil` is unusually
repetitive, which is what made `dense` a good ambiguity fixture and may make
arm A's failure rate here higher than it would be elsewhere.

The formatting failures are a Go-specific *diagnosis* — gofmt makes them visible
— but the underlying error is not: a model that mis-indents Go by one tab will
mis-indent Python by four spaces, where it changes the meaning rather than the
formatting. That is worth knowing before anyone reads this result as "anchored
editing is fine in a language without gofmt".
