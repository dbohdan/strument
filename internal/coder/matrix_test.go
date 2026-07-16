// The basecoder-spec §11 failure/invariant matrix, as inline fixture
// scenarios plus targeted tests where a fixture can't drive the condition
// (interrupts, clocks).

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbohdan/strument/internal/fixture"
	"github.com/dbohdan/strument/internal/llm"
)

// fastClock never sleeps; it records requested delays.
type fastClock struct{ slept []time.Duration }

func (f *fastClock) Sleep(d time.Duration) { f.slept = append(f.slept, d) }
func (f *fastClock) Now() time.Time        { return time.Unix(0, 0) }

// fakeRepo provides tracked files so file mentions have addable targets.
type fakeRepo struct{ tracked []string }

func (r *fakeRepo) Root() string             { return "" }
func (r *fakeRepo) TrackedFiles() []string   { return r.tracked }
func (r *fakeRepo) PathInRepo(_ string) bool { return true }
func (r *fakeRepo) IsDirty(_ string) bool    { return false }
func (r *fakeRepo) GitIgnored(_ string) bool { return false }
func (r *fakeRepo) HeadSHA() string          { return "deadbeef" }
func (r *fakeRepo) Commit([]string, string) (string, string, bool, error) {
	return "", "", false, nil
}

// yesConfirmer answers yes to everything (including explicit-yes prompts).
type yesConfirmer struct{}

func (yesConfirmer) Confirm(ConfirmRequest) (bool, bool) { return true, false }

// noConfirmer declines everything.
type noConfirmer struct{}

func (noConfirmer) Confirm(ConfirmRequest) (bool, bool) { return false, false }

const metaRow = `{"v":1,"kind":"meta","scenario":"inline","source":"authored"}`

func TestContinuationStitch(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"part1"},{"kind":"Finish","finish_reason":"length"},{"kind":"Usage","usage":{"prompt_tokens":10,"completion_tokens":5}}]}
{"kind":"stream","events":[{"kind":"Answer","text":" part2"},{"kind":"Finish","finish_reason":"stop"},{"kind":"Usage","usage":{"prompt_tokens":12,"completion_tokens":6}}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_usage","sent":22,"received":11,"cost_known":false}
`)
	env := setupScenario(t, sc, nil)
	var prefills []string
	env.stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "assistant" {
			prefills = append(prefills, last.Text())
		}
		return nil
	}
	env.run(t)
	if env.coder.partialResponseContent != "part1 part2" {
		t.Errorf("stitched answer = %q", env.coder.partialResponseContent)
	}
	if len(prefills) != 1 || prefills[0] != "part1" {
		t.Errorf("prefills = %q", prefills)
	}
}

func TestSecondContinuationReplacesPrefill(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"A"},{"kind":"Finish","finish_reason":"length"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"B"},{"kind":"Finish","finish_reason":"length"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"C"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	var prefills []string
	var assistantCounts []int
	env.stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		count := 0
		for _, m := range req.Messages {
			if m.Role == "assistant" && strings.HasPrefix(m.Text(), "A") {
				count++
				prefills = append(prefills, m.Text())
			}
		}
		assistantCounts = append(assistantCounts, count)
		return nil
	}
	env.run(t)
	// Second continuation must REPLACE the prefill, not stack a second one.
	if len(prefills) != 2 || prefills[0] != "A" || prefills[1] != "AB" {
		t.Errorf("prefills = %q", prefills)
	}
	if assistantCounts[2] != 1 {
		t.Errorf("turn 3 has %d prefill messages, want 1", assistantCounts[2])
	}
	if env.coder.partialResponseContent != "ABC" {
		t.Errorf("answer = %q", env.coder.partialResponseContent)
	}
}

func TestContinuationCapOutputExhausted(t *testing.T) {
	rows := metaRow + "\n" + `{"kind":"user","text":"go"}` + "\n"
	var rowsSb112 strings.Builder
	for range 6 {
		rowsSb112.WriteString(`{"kind":"stream","events":[{"kind":"Answer","text":"x"},{"kind":"Finish","finish_reason":"length"}]}` + "\n")
	}
	rows += rowsSb112.String()
	rows += `{"kind":"expect_outcome","outcome":"OutputExhausted","reflections":0}`
	sc := inlineScenario(t, rows)
	env := setupScenario(t, sc, nil)
	env.run(t)
	// Cap is 4 continuations => 5 attempts consumed, the partial kept, and
	// the trailing history message is the assistant partial (no diagnostic).
	if env.stub.Remaining() != 1 {
		t.Errorf("consumed %d turns, want 5", 6-env.stub.Remaining())
	}
	hist := env.coder.curMessages
	if len(hist) != 2 || hist[1].Role != "assistant" || hist[1].Text() != "xxxxx" {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestContinuationWithoutPrefillSupport(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"partial"},{"kind":"Finish","finish_reason":"length"}]}
{"kind":"expect_outcome","outcome":"OutputExhausted","reflections":0}
`)
	env := setupScenario(t, sc, func(c *Coder) { c.PrefillSupported = false })
	env.run(t)
	if env.stub.Remaining() != 0 {
		t.Error("should not attempt continuation without prefill support")
	}
}

func TestRetryDiscardsPartialAndSumsUsage(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"junk"},{"kind":"Usage","usage":{"prompt_tokens":7,"completion_tokens":2}},{"kind":"Error","class":"network","message":"reset"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"clean"},{"kind":"Finish","finish_reason":"stop"},{"kind":"Usage","usage":{"prompt_tokens":8,"completion_tokens":3}}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_usage","sent":15,"received":5,"cost_known":false}
`)
	clock := &fastClock{}
	env := setupScenario(t, sc, func(c *Coder) { c.Clock = clock })
	env.run(t)
	if env.coder.partialResponseContent != "clean" {
		t.Errorf("answer = %q (retry must discard the partial)", env.coder.partialResponseContent)
	}
	// First real sleep is 0.25s (delay doubles before sleeping, §2.1).
	if len(clock.slept) != 1 || clock.slept[0] != 250*time.Millisecond {
		t.Errorf("slept = %v", clock.slept)
	}
	hist := env.coder.curMessages
	if len(hist) != 2 || hist[1].Text() != "clean" {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestFailedAfterPartialKeepsHistoryBytes(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"partial thoughts"},{"kind":"Error","class":"auth","message":"bad key"}]}
{"kind":"expect_outcome","outcome":"Failed","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	hist := env.coder.curMessages
	if len(hist) != 2 || hist[0].Role != "user" || hist[1].Role != "assistant" || hist[1].Text() != "partial thoughts" {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestEmptyStreamDoesNotPoisonHistory(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Failed","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	if len(env.coder.curMessages) != 0 {
		t.Errorf("history should be clean: %s", dumpHistory(env.coder.curMessages))
	}
}

func TestContextExhaustedEmptyAddsDiagnostic(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Error","class":"context_window","message":"too long"}]}
{"kind":"expect_outcome","outcome":"ContextExhausted","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	hist := env.coder.curMessages
	if len(hist) != 2 || hist[1].Role != "assistant" ||
		hist[1].Text() != "FinishReasonLength exception: you sent too many tokens" {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestContextExhaustedWithPartialKeepsPartial(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"some work"},{"kind":"Error","class":"context_window","message":"too long"}]}
{"kind":"expect_outcome","outcome":"ContextExhausted","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	hist := env.coder.curMessages
	if len(hist) != 2 || hist[1].Text() != "some work" {
		t.Errorf("history = %s (no diagnostic when partial present)", dumpHistory(hist))
	}
}

func TestCheckTokensDeclined(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"expect_outcome","outcome":"Failed","reflections":0}
`)
	env := setupScenario(t, sc, func(c *Coder) {
		c.Model.Context = 10 // tiny window; the prompt alone exceeds it
		c.Confirm = noConfirmer{}
	})
	env.run(t)
	if env.stub.Remaining() != 0 {
		// zero turns in the fixture; a send would have failed loudly anyway
		t.Error("no request should have been sent")
	}
	if len(env.coder.curMessages) != 0 {
		t.Errorf("declined checkTokens must not poison history: %s", dumpHistory(env.coder.curMessages))
	}
}

// H1 regression: a reflection after a continuation-bearing send must not
// carry a stale accumulator prefix into the next send's answer.
func TestStaleAccumulatorRegression(t *testing.T) {
	badEdit := "a.txt\\n<<<<<<< SEARCH\\nno such line\\n=======\\nnew\\n>>>>>>> REPLACE\\n"
	sc := inlineScenario(t, metaRow+`
{"kind":"fs","path":"a.txt","content":"hello\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+badEdit[:20]+`"},{"kind":"Finish","finish_reason":"length"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"`+badEdit[20:]+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"understood, no edit"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":1}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	if env.coder.partialResponseContent != "understood, no edit" {
		t.Errorf("send 2 answer = %q (stale accumulator?)", env.coder.partialResponseContent)
	}
	if env.coder.multiResponseContent != "" {
		t.Errorf("multiResponseContent = %q, want empty", env.coder.multiResponseContent)
	}
}

// cancelingOutput cancels the context after the first streamed delta,
// simulating Ctrl-C mid-stream.
type cancelingOutput struct {
	testOutput

	cancel context.CancelFunc
	fired  bool
}

func (o *cancelingOutput) StreamText(_ string) {
	if !o.fired {
		o.fired = true
		o.cancel()
	}
}

func TestInterruptMidStream(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"partial answer"},{"kind":"Answer","text":" more"}]}
`)
	ctx, cancel := context.WithCancel(context.Background())
	env := setupScenario(t, sc, nil)
	env.coder.Out = &cancelingOutput{testOutput{t}, cancel, false}
	env.coder.Run(ctx, sc.User)

	if env.coder.lastSendOutcome != OutcomeInterrupted {
		t.Errorf("outcome = %s", env.coder.lastSendOutcome)
	}
	hist := env.coder.curMessages
	// [Exact] shape: user, assistant partial, user ^C, assistant notice.
	if len(hist) != 4 ||
		hist[1].Role != "assistant" || hist[1].Text() != "partial answer" ||
		hist[2].Role != "user" || hist[2].Text() != "^C KeyboardInterrupt" ||
		hist[3].Text() != "I see that you interrupted my previous reply." {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestInterruptThenMentionDoesNotReflect(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"fs","path":"main.go","content":"package main\n"}
{"kind":"fs","path":"other.go","content":"package main\n"}
{"kind":"chat","editable":["main.go"]}
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"you should add other.go to the chat"},{"kind":"Answer","text":" ..."}]}
`)
	ctx, cancel := context.WithCancel(context.Background())
	env := setupScenario(t, sc, func(c *Coder) {
		c.Repo = &fakeRepo{tracked: []string{"main.go", "other.go"}}
	})
	env.coder.Out = &cancelingOutput{testOutput{t}, cancel, false}
	env.coder.Run(ctx, sc.User)
	if env.coder.lastSendOutcome != OutcomeInterrupted {
		t.Errorf("outcome = %s (mention must not reflect after interrupt)", env.coder.lastSendOutcome)
	}
	if env.coder.numReflections != 0 {
		t.Errorf("reflections = %d", env.coder.numReflections)
	}
}

func TestDuplicateShellBlocksRunOnce(t *testing.T) {
	answer := "```bash\\necho hi\\n```\\n\\nagain:\\n\\n```bash\\necho hi\\n```\\n"
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"confirm","prompt":"Run shell command?","answer":"y"}
{"kind":"confirm","prompt":"Add command output to the chat?","answer":"y"}
{"kind":"command","block":"echo hi","exit":0,"output":"hi\n"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+answer+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	if len(env.coder.shellCommands) != 1 {
		t.Errorf("shellCommands = %q (dup should collapse)", env.coder.shellCommands)
	}
	// Output appended as {user, output} + {assistant, "Ok"}.
	hist := env.coder.curMessages
	n := len(hist)
	if n < 2 || hist[n-1].Text() != "Ok" || !strings.Contains(hist[n-2].Text(), "Exit status: 0") {
		t.Errorf("history = %s", dumpHistory(hist))
	}
}

func TestSuggestShellOffGatesExecution(t *testing.T) {
	answer := "```bash\\nrm -rf /\\n```\\n"
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+answer+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, func(c *Coder) { c.SuggestShellCommands = false })
	env.run(t)
	// No confirm rows scripted: any confirm attempt would have failed the
	// test. The block is still collected (models emit them anyway).
	if len(env.coder.shellCommands) != 1 {
		t.Errorf("shellCommands = %q", env.coder.shellCommands)
	}
}

func TestShellFromFailedAttemptRunsAfterReflectedSuccess(t *testing.T) {
	attempt1 := "```bash\\necho from-attempt-1\\n```\\n\\na.txt\\n<<<<<<< SEARCH\\nno such\\n=======\\nx\\n>>>>>>> REPLACE\\n"
	attempt2 := "a.txt\\n<<<<<<< SEARCH\\nhello\\n=======\\ngoodbye\\n>>>>>>> REPLACE\\n"
	sc := inlineScenario(t, metaRow+`
{"kind":"fs","path":"a.txt","content":"hello\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"go"}
{"kind":"confirm","prompt":"Run shell command?","answer":"y"}
{"kind":"confirm","prompt":"Add command output to the chat?","answer":"n"}
{"kind":"command","block":"echo from-attempt-1","exit":0,"output":"from-attempt-1\n"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+attempt1+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"`+attempt2+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_fs","path":"a.txt","content":"goodbye\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":1}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
}

func TestReflectionCapFourSends(t *testing.T) {
	bad := "a.txt\\n<<<<<<< SEARCH\\nno such\\n=======\\nx\\n>>>>>>> REPLACE\\n"
	rows := metaRow + "\n" +
		`{"kind":"fs","path":"a.txt","content":"hello\n"}` + "\n" +
		`{"kind":"chat","editable":["a.txt"]}` + "\n" +
		`{"kind":"user","text":"go"}` + "\n"
	var rowsSb389 strings.Builder
	for range 5 {
		rowsSb389.WriteString(`{"kind":"stream","events":[{"kind":"Answer","text":"` + bad + `"},{"kind":"Finish","finish_reason":"stop"}]}` + "\n")
	}
	rows += rowsSb389.String()
	sc := inlineScenario(t, rows)
	env := setupScenario(t, sc, nil)
	env.run(t)
	// Budget: initial + 3 follow-ups = 4 sends; the 5th fixture turn stays.
	if got := 5 - env.stub.Remaining(); got != 4 {
		t.Errorf("sends = %d, want 4", got)
	}
	if env.coder.numReflections != 3 {
		t.Errorf("reflections = %d, want 3", env.coder.numReflections)
	}
}

func TestNoEditTurnKeepsRecentHistory(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"what is this?"}
{"kind":"stream","events":[{"kind":"Answer","text":"just a file"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_history","messages":[{"role":"user","text":"what is this?"},{"role":"assistant","text":"just a file"}]}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	if len(env.coder.doneMessages) != 0 {
		t.Error("no-edit turn must not rotate history")
	}
}

func TestDryRunSuppressesWrites(t *testing.T) {
	edit := "a.txt\\n<<<<<<< SEARCH\\nhello\\n=======\\ngoodbye\\n>>>>>>> REPLACE\\n"
	sc := inlineScenario(t, metaRow+`
{"kind":"fs","path":"a.txt","content":"hello\n"}
{"kind":"chat","editable":["a.txt"]}
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+edit+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_fs","path":"a.txt","content":"hello\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, func(c *Coder) { c.DryRun = true })
	env.run(t)
}

func TestPathTraversalRejected(t *testing.T) {
	evil := "../evil.txt\\n<<<<<<< SEARCH\\n=======\\npwned\\n>>>>>>> REPLACE\\n"
	sc := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+evil+`"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
`)
	env := setupScenario(t, sc, nil)
	env.run(t)
	if _, err := os.ReadFile(filepath.Join(env.dir, "..", "evil.txt")); err == nil {
		t.Error("path traversal wrote outside the root")
	}
	absEvil := "/tmp/strument-evil-abs.txt\\n<<<<<<< SEARCH\\n=======\\npwned\\n>>>>>>> REPLACE\\n"
	sc2 := inlineScenario(t, metaRow+`
{"kind":"user","text":"go"}
{"kind":"stream","events":[{"kind":"Answer","text":"`+absEvil+`"},{"kind":"Finish","finish_reason":"stop"}]}
`)
	env2 := setupScenario(t, sc2, nil)
	env2.run(t)
	if _, err := os.Stat("/tmp/strument-evil-abs.txt"); err == nil {
		t.Error("absolute path wrote outside the root")
	}
}

func TestDeclinedMentionNotReprompted(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"fs","path":"main.go","content":"package main\n"}
{"kind":"fs","path":"other.go","content":"package main\n"}
{"kind":"chat","editable":["main.go"]}
{"kind":"user","text":"go"}
{"kind":"confirm","prompt":"Add file to the chat?","answer":"n"}
{"kind":"stream","events":[{"kind":"Answer","text":"look at other.go please"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"other.go again I say"},{"kind":"Finish","finish_reason":"stop"}]}
`)
	env := setupScenario(t, sc, func(c *Coder) {
		c.Repo = &fakeRepo{tracked: []string{"main.go", "other.go"}}
	})
	// First turn: mention declined (one confirm row consumed).
	env.coder.Run(context.Background(), sc.User)
	// Second turn: same mention; an unscripted confirm would fail the test.
	env.coder.Run(context.Background(), "go again")
	if env.coder.numReflections != 0 {
		t.Errorf("reflections = %d", env.coder.numReflections)
	}
}

func TestMentionReflects(t *testing.T) {
	sc := inlineScenario(t, metaRow+`
{"kind":"fs","path":"main.go","content":"package main\n"}
{"kind":"fs","path":"helper.go","content":"package main\n"}
{"kind":"chat","editable":["main.go"]}
{"kind":"user","text":"go"}
{"kind":"confirm","prompt":"Add file to the chat?","answer":"y"}
{"kind":"stream","events":[{"kind":"Answer","text":"I need helper.go added"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"stream","events":[{"kind":"Answer","text":"thanks, all set"},{"kind":"Finish","finish_reason":"stop"}]}
{"kind":"expect_outcome","outcome":"Success","reflections":1}
`)
	env := setupScenario(t, sc, func(c *Coder) {
		c.Repo = &fakeRepo{tracked: []string{"main.go", "helper.go"}}
	})
	var secondUser string
	env.stub.OnRequest = func(turn int, req llm.Request, _ *fixture.Request) error {
		if turn == 1 {
			for _, m := range req.Messages {
				if m.Role == "user" && strings.Contains(m.Text(), "added these files") {
					secondUser = m.Text()
				}
			}
		}
		return nil
	}
	env.run(t)
	if !strings.Contains(secondUser, "I added these files to the chat: helper.go") {
		t.Errorf("reflection message = %q", secondUser)
	}
}
