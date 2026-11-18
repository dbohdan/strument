# Tables and angle brackets in fetched pages

**Status: characterization, run 2026-11-18.** A spot check after a converter
fix, not a preregistered trial. In short:
- `webfetch` flattened every HTML table into one line and turned `<` and `>`
  in page text into `&lt;` and `&gt;`. Both are fixed (`1e1904c`), and turning
  tables on exposed a third fault, a caption written under its table, where it
  read as the next table's caption. That is fixed too.
- Against Hy4 flash on the task that showed the fault, the fix did not reduce
  the work: median `run_code` calls were 3.5 before and 4.5 after, and all 16
  sessions produced correct tables. What cost Hy4 flash its programs was its
  own regular expressions.
- The fix is kept because what the model was handed was wrong, not because it
  is measured to help.

## What happened

In a user's session, Hy4 flash (`tencent/hy4-flash`) was asked to build the
SRS wall kick tables in C++ from `https://tetris.wiki/SRS#Wall_Kicks`. It
fetched the page from `run_code` six times across six programs, four of which
failed, before its tables came out right.

Fetching the same page through Strument showed two faults in the HTML-to-Markdown
conversion:
- **Tables lost their rows.** The converter ran with only its base and
  CommonMark plugins, so each eight-row kick table came out as a single line of
  about 600 characters, caption, header and rows run together.
- **`<` and `>` arrived escaped.** The base plugin writes them as `&lt;` and
  `&gt;` so a Markdown renderer cannot take them for HTML. Nothing renders
  `webfetch` output; a model reads it. `0->R` reached the model as `0-&gt;R`.
  The Wikipedia article on Go had two such escapes; any documentation page
  with an arrow, a comparison or a type parameter in its prose has them.

The fix enables the table plugin with minimal cell padding. A table with a line
break in a cell is still left as text, which keeps layout tables from becoming
one enormous row. `<` and `>` in text are swapped for noncharacters before the
base plugin escapes them and restored afterwards; a page whose text reads
`&lt;` (from `&amp;lt;`) still reads `&lt;`, which unescaping after conversion
would have broken.

Turning tables on surfaced a third fault, worse than either: the plugin writes
a table's `<caption>` after the table. On the SRS page the I tetromino's kick
values then sat under the caption "J, L, S, T, Z Tetromino Wall Kick Data", and
the I caption headed nothing. That is wrong data that looks authoritative.
Captions are now moved in front of their tables before conversion.

Side by side on three pages, old converter against new:

| page | bytes before → after | change |
| --- | --- | --- |
| tetris.wiki SRS | 3,247 → 3,603 | both kick tables as tables, one row per line, captions above them |
| Hacker News front page | 14,049 → 15,174 | layout tables become tables; each story is a row |
| Wikipedia, Go | 114,158 → 114,174 | infobox fields one to a paragraph instead of run together; two `&gt;`/`&lt;` gone |
| Go specification, `#Operators` | 3,979 → 3,979 | none |

## The live check

The task from the user's session, as one message: use `run_code` with
`webfetch` on the SRS page to generate both kick tables as C++, rows in a
given order, printed and not written.

- **Arms:** the binary before the fix (`old`) and after it (`new`), 8 sessions
  each. Launch order shuffled with a fixed seed (`data/order.txt`), four at a
  time.
- **Model:** Hy4 flash, `reasoning = "low"`, through OpenRouter.
- **Setting:** `chat --unattended --yes webfetch`, a fresh empty repository and
  state directory per session.
- **Measures:** counts from the transcript: `run_code` calls, failed programs,
  and whether each final table equals the page's (`data/score.py`, which reads
  the reference rows from the page as the new converter renders it).

| arm | `run_code` calls per session | median | failed programs | correct tables |
| --- | --- | --- | --- | --- |
| old | 2, 3, 3, 3, 4, 4, 8, 21 | 3.5 | 13 | 8/8 |
| new | 3, 3, 4, 4, 5, 7, 7, 7 | 4.5 | 8 | 8/8 |

No improvement. Eight sessions per arm cannot rule out a small one either way,
and the old arm's total rests on one 21-call session.

Reading the new arm's failures: every one is Hy4 flash's own regular
expression meeting a clean table. Patterns like `\d+->[R L2]+` cannot match
`R->0`, and cell patterns that allow no backticks miss `` `( 0, 0)` ``. The
user's original session failed on the same backticks (`[^(\`]*`). The
flattening and the escaping made that harder; they were not the cause.

Cost: about $0.016 for all 16 sessions.

## Not done

- Every program refetches the page: six fetches in the user's session, two in
  some of these. A turn-scoped cache keyed by URL would make iterating faster,
  but a model checking a local server after an edit needs a fresh fetch.
- The same session failed once on `"\033"` in a template literal holding C++
  for an edit. That got an error hint (`f4e4591`) and, separately, a trial of a
  sentence in the `run_code` description.

## Files

- `data/run.sh`: one session, given an arm and an index.
- `data/score.py`: the scorer.
- `data/config.star`, `data/order.txt`: the model configuration and the launch
  order.
- `data/scores.txt`: one line per session.
- Raw transcripts and session records:
  [`dbohdan/experimental-data` at `0771fdc`](https://github.com/dbohdan/experimental-data/tree/0771fdc/strument/2026-11-webfetch-tables).
