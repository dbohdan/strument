# Tool-call errors inside `run_code`: the traceback that wasn't

2026-09-01. Not a trial — a bug report from a live session, a fix, and the
lesson that the fix's *stated rationale* was the thing most worth checking.
Recorded because the arc — comment claimed X, probe showed not-X, fix changed
semantics — is the arc this directory exists to preserve, and because the
fix's second-order effect (catchability) was found by a test, not by design.

## The report

In a smoke run of the `code` tool (then `code`; now `run_code`), a program
called `read(path=g[0])` with a bad path and got back:

```
Could not read 3: stat .../3: no such file or directory
```

One line, no line number, no indication of *which* of the program's tool
calls produced it. The model's own report said the thing that generalizes:
with a 3-line program it could debug by eye; in a 20-line program that costs
real time. A cheap improvement was proposed — "file + line of the failing
tool call would compound across every future session".

## What the code claimed vs. what it did

The bridge's comment (`internal/coder/codetool.go`, `bridgeCall`) said the
failure "becomes the exception message and Monty's traceback names the
program line that made the call". **This was false, and nothing in the suite
could catch it being false** — no test ran a program with multiple tool
calls where a later one fails.

A probe (a throwaway test driving `runCode` directly) showed the split:

- Python-level errors (`x[5]`, `NameError`) came back with full tracebacks —
  `File "script.py", line 2`, the failing source line, the `~~~~` underline.
- Tool-call failures came back as a single flattened line, no traceback, no
  line number.

## Root cause

The Go wrapper (`internal/monty/wasm.go`) caught the bridge handler's error
and **returned immediately** — abandoning the snapshot. The interpreter never
saw the failure, so there was nothing to traceback. Monty's machinery for
exactly this — resume the snapshot *with* an exception, which the shim
already used for its auto-`NameError` path — was never invoked.

## The fix

A new C-ABI export, `monty_resume_error(snapshot_handle, err_json_ptr,
err_json_len)`, which resumes the snapshot with
`ExtFunctionResult::Error` — the exception re-enters the interpreter, is
raised at the call site, and produces a traceback naming the line. The Go
side routes external-function handler failures through it. The wire protocol
grows one export and one JSON shape (`{"type": "RuntimeError", "message":
…}`); nothing else moves. **Only a hard fork made this available** — under
the old diff-against-upstream arrangement, the shim's C-ABI surface was
effectively frozen and the remedy would have been a worse, Go-side
approximation.

After:

```
Traceback (most recent call last):
  File "script.py", line 5, in <module>
    b = read(path=p + "/deep")
        ~~~~~~~~~~~~~~~~~~~~~~
RuntimeError: external function "read" failed: read failed: Could not read …
```

## The second-order effect: errors became catchable

Once a tool failure re-enters the interpreter as an exception, it is an
ordinary Python exception — `try: read(...) except Exception:` works. That
was not part of the goal, and a test caught it: `TestCodeBridgeCapFires`
failed after the change because its program *caught* the bridged-call cap
error and continued looping.

The resolution is deliberate, not accidental: the cap (50 calls) is a guide,
not a wall. A `while True` + `try/except` loop that swallows the cap error
runs to the 5-second duration limit, which bounds it (verified by probe).
The cap's error message was also reworded — "stop and do the rest with
direct tool calls" prescribed a strategy written for the kill-the-program
world; a task needing 120 lookups is better served by a second program, and
which one is the model's judgment. The message now states the constraint
only. `TestCodeErrorIsCatchable` pins the catchability;
`TestCodeBridgeCapFires` pins the cap in its uncaught form with a comment
explaining why uncaught.

## What to conclude

- **The comment was the bug's best hiding place.** The claim "traceback names
  the program line" was written as rationale, read as fact, and survived
  because no test exercised it. Anywhere a comment asserts behavior, that is
  a test request.
- **The fix was only reachable because the shim is owned.** `monty_resume_error`
  is a fork addition to the C-ABI; the frozen-wire-format rule in
  `internal/monty/shim/README.md` existed precisely to stop this change being
  made casually — under the old arrangement it would have stopped it entirely.
- **Attribute errors at the layer that has the information.** The line number
  lives in the interpreter's stack, so the error must re-enter the
  interpreter to acquire it. Stitching it on in Go would have meant parsing
  programs — fragile, and the wrong layer.
- **A fix that changes semantics needs a test that would have failed on
  purpose.** The catchability was invisible until a cap test broke. That
  failure was the system working, not a bug in the fix.
