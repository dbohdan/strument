# Spec: aider's editblock (SEARCH/REPLACE) edit format

Source of truth: `aider/coders/editblock_coder.py` at 5dc9490bb35f9729ef2c95d00a19ccd30c26339c (0.86.3.dev), 657 lines.
Companion prompt contract: `aider/coders/editblock_prompts.py`.
Test oracle: `tests/basic/test_editblock.py` (618 lines) and `tests/basic/test_find_or_blocks.py`.

The pipeline has four stages: **parse** the LLM response into blocks, **resolve** each block's filename, **apply** each block with a matching ladder, and **report** failures in a format designed for the reflection loop.

---

## 1. Block syntax

Marker lines, matched against the *stripped* line (leading/trailing whitespace ignored), as anchored regexes:

```
HEAD:    ^<{5,9} SEARCH>?\s*$
DIVIDER: ^={5,9}\s*$
UPDATED: ^>{5,9} REPLACE\s*$
```

Notes:

- 5 to 9 repeated marker characters are accepted (models miscount).
- `SEARCH>` with a trailing `>` is accepted (observed model quirk).
- Marker matching is on `line.strip()`, so indented markers count.

Canonical shape as taught by the system prompt:

```
path/to/file.py
{fence[0]}
<<<<<<< SEARCH
old lines
=======
new lines
>>>>>>> REPLACE
{fence[1]}
```

`fence` defaults to `("```", "```")` but is dynamic: `base_coder.choose_fence()` scans in-chat file contents and escalates to longer/alternative fences (````` ```` `````, `<source>` ... ) when files themselves contain backticks. The parser and wrapping-stripper must take the fence pair as a parameter.

## 2. Parsing: `find_original_update_blocks(content, fence, valid_fnames) -> iterator`

Line-by-line scan over `content.splitlines(keepends=True)` with index `i` and a sticky `current_filename`. Yields two kinds of items:

- `(None, shell_text)` — a suggested shell command block.
- `(filename, original_text, updated_text)` — an edit block.

### 2.1 Shell blocks

If a line starts (after strip) with one of:

```
```bash ```sh ```shell ```cmd ```batch ```powershell ```ps1 ```zsh ```fish ```ksh ```csh ```tcsh
```

**and** neither of the next two lines matches HEAD (guards against a shell-fenced edit block), consume lines until a line starting with ```` ``` ````, skip the closing fence, and yield `(None, joined_content)`. The coder collects these into `shell_commands` and offers to run them after edits are applied.

### 2.2 Edit blocks

When a line matches HEAD:

1. **Filename lookahead for new files:** if the *very next* line matches DIVIDER, the SEARCH section is empty (new-file idiom); resolve the filename with `valid_fnames=None` — i.e., accept any plausible filename, not just in-chat files. Otherwise resolve with the provided `valid_fnames` (the in-chat relative paths).
2. Resolution examines up to the **3 lines preceding** the HEAD line (see §3). If nothing resolves, fall back to `current_filename` (the last successfully resolved filename in this response). If there is none, error: `"Bad/missing filename. The filename must be alone on the line before the opening fence ..."`.
3. Set `current_filename = filename`.
4. Collect SEARCH lines until a DIVIDER line. EOF before DIVIDER → error ``Expected `=======` ``.
5. Collect REPLACE lines until a line matching **UPDATED or DIVIDER**. EOF first → error ``Expected `>>>>>>> REPLACE` or `=======` ``. Accepting DIVIDER as a terminator tolerates truncated/chained blocks.
6. Yield `(filename, "".join(search_lines), "".join(replace_lines))`. Sections keep their line endings; markers are excluded.

Parse errors are raised as `ValueError` whose message embeds everything processed so far plus `^^^ <err>` — this text goes straight back to the model. Preserve it.

## 3. Filename resolution: `find_filename(preceding_lines, fence, valid_fnames)`

Scan the ≤3 preceding lines in reverse (nearest first). For each line, extract a candidate with `strip_filename` (§3.1); stop scanning as soon as a line does **not** start with `fence[0]` or with ```` ``` ```` (i.e., you may hop over fence-opening lines to find the filename above them — this handles the DeepSeek pattern of `filename` / ```` ```python ```` / `<<<<<<< SEARCH`).

From the collected candidates, pick the best in this priority order:

1. Exact match against `valid_fnames`.
2. Basename match: candidate equals `Path(vfn).name` of some valid file → return the full valid path.
3. Fuzzy: `difflib.get_close_matches(candidate, valid_fnames, n=1, cutoff=0.8)` — in Go, replicate SequenceMatcher ratio or substitute a similarity function and pin with tests.
4. First candidate containing a `.` (looks like a filename with extension).
5. First candidate.

### 3.1 `strip_filename(line, fence)`

- Strip whitespace. A bare `...` is **not** a filename (returns nothing).
- If the line starts with `fence[0]` or ```` ``` ````: the remainder is a candidate only if it contains `.` or `/` (handles ```` ```python path/to/file.py ````); otherwise nothing.
- Else: strip trailing `:`, leading `#`, surrounding whitespace, surrounding `` ` `` and `*` (Markdown decoration).

## 4. Application

### 4.1 Per-edit driver (`apply_edits`)

For each `(path, original, updated)`:

1. `full_path = abs_root_path(path)`; if the file exists, read it and call `do_replace` (§4.2).
2. **Cross-file retry:** if the replace failed *and* `original` is non-blank, retry `do_replace` against every other in-chat file; on the first success, reattribute the edit to that file. (Models regularly put the right block under the wrong filename.)
3. Success → write file (unless dry run). Failure → collect for the error report (§5).

Edits are applied sequentially and the file is re-read for each edit, so multiple blocks against one file compose in order.

**Uniqueness caveat:** matching is leftmost-first; a SEARCH text occurring multiple times silently edits the first occurrence. Production aider does not enforce uniqueness for this format. Decide deliberately whether to keep this or fail on ambiguity; keeping it preserves prompt-observed behavior.

### 4.2 `do_replace(fname, content, before_text, after_text, fence)`

1. Run both sections through `strip_quoted_wrapping` (§4.3).
2. **New file:** if `fname` does not exist and `before_text.strip()` is empty → create the file, treat content as `""`.
3. **Append:** if `before_text.strip()` is empty (file exists) → `new_content = content + after_text`. Empty SEARCH on an existing file means *append*, not *replace-all*. This is load-bearing; models use it to add code to file ends.
4. Otherwise → `replace_most_similar_chunk(content, before_text, after_text)` (§4.4). Returns new content or nothing.

### 4.3 `strip_quoted_wrapping(text, fname, fence)`

Removes redundant wrapping the model sometimes puts *inside* a section: if the first line ends with the file's basename, drop it; then if first line starts with `fence[0]` and last line starts with `fence[1]`, drop both. Ensure trailing newline.

### 4.4 The matching ladder: `replace_most_similar_chunk(whole, part, replace)`

Preprocessing (`prep`): ensure each text ends with `\n`; split into lines keeping line endings.

Try in order; first success returns the new whole-file content:

**Step 1 — perfect match.** Slide a window of `len(part_lines)` over `whole_lines`; compare as line tuples (exact, including all whitespace). On match, splice in `replace_lines`.

**Step 2 — uniform-leading-whitespace match.** Handles the model omitting or truncating indentation *uniformly* across the block:

1. Compute `num_leading` = min leading-whitespace length over all non-blank lines of `part_lines` **and** `replace_lines` combined. If > 0, outdent both by exactly that many characters (blank lines untouched).
2. Slide the window. A window matches iff for every line `whole[i].lstrip() == part[i].lstrip()`, **and** the set of removed prefixes `whole_line[: len(whole_line) - len(part_line)]` over non-blank lines has exactly one element (a single uniform indent string, spaces or tabs).
3. Re-indent every non-blank `replace` line with that prefix and splice.

**Step 3 — retry 1–2 without a spurious leading blank line.** If `len(part_lines) > 2` and the first line is blank, drop it and rerun steps 1–2. (GPT adds phantom blank lines; issue #25.)

**Step 4 — `...` elision (`try_dotdotdots`).** Regex-split both `part` and `replace` on lines of the form `^\s*\.\.\.\n` (multiline):

- If `part` has no `...`, this step doesn't apply.
- The two splits must have the same piece count, and the `...` separator strings at odd indices must be pairwise identical; otherwise the step *raises* (treated as no-match by the caller).
- For each corresponding non-dots pair `(p, r)`: both empty → skip; `p` empty and `r` non-empty → append `r` to the whole (newline-terminated); otherwise `p` must occur **exactly once** in the whole (`count == 1`, string containment — not line-aligned) → `whole.replace(p, r, 1)`. Zero or multiple occurrences → raise → no match.

**Step 5 — fuzzy similarity match: DEAD CODE.** `replace_closest_edit_distance` (SequenceMatcher ratio ≥ 0.8 over windows of `len(part_lines)` ± 10%) sits *after an unconditional `return`* in the source. Production aider never runs it. Omit from the port; the reflection loop plus §5's did-you-mean report replaces it. Documented here only so you don't rediscover it and think it matters.

## 5. Failure report (prompt engineering — port verbatim)

If any edits failed, raise an error whose text the coder feeds back to the model as the next user message:

```
# {n} SEARCH/REPLACE block(s) failed to match!

## SearchReplaceNoExactMatch: This SEARCH block failed to exactly match lines in {path}
<<<<<<< SEARCH
{original}=======
{updated}>>>>>>> REPLACE
```

Then, when applicable:

- **Did-you-mean:** `find_similar_lines(original, file_content)` — SequenceMatcher over *lists of lines*, window = `len(search_lines)`, best ratio ≥ 0.6. If the best chunk's first and last lines equal the search's, return it as-is; otherwise expand the window by 5 lines on each side. Emit inside the fence pair under `Did you mean to match some of these actual lines from {path}?`.
- **Already applied:** if `updated` is non-empty and already present in the file: `Are you sure you need this SEARCH/REPLACE block? The REPLACE lines are already in {path}!`
- Always: `The SEARCH section must exactly match an existing block of lines including all white space, comments, indentation, docstrings, etc`
- If some blocks passed: `# The other {k} SEARCH/REPLACE block(s) were applied successfully. Don't re-send them. Just reply with fixed versions of the block(s) above that failed to match.`

The coder catches this, increments the reflection counter (`max_reflections = 3`), and sends the text as the next message.

## 6. The other half of the contract: the prompts

The parser's tolerance and the system prompt's rules co-evolved. Port `editblock_prompts.py` verbatim: the rules about "every SEARCH must exactly match", the full-path-alone-on-a-line requirement, the new-file idiom (empty SEARCH), the move-code idiom (two blocks: delete + insert), shell-command suggestions, and the few-shot examples. `editblock_fenced` differs only in placing the fence *outside* the filename line; it's a prompt swap plus the already-fence-aware parser. `wholefile` is a separate trivial format: filename line, fenced full file content, overwrite.

## 7. Port checklist

1. Data types: `Edit = {path string; search, replace string}` plus a shell-command list. Keep sections as raw strings with line endings.
2. Implement §2 parser; transliterate `test_find_or_blocks.py` and the parser cases in `test_editblock.py` as Go table tests first (the oracle).
3. Implement §3 filename resolution with a pinned similarity function.
4. Implement §4 ladder exactly: steps 1–4, no fuzzy step.
5. Implement §5 report strings byte-for-byte.
6. SequenceMatcher: needed twice (filename fuzzy @0.8 on strings, did-you-mean @0.6 on line lists). Either port difflib's ratio (Ratcliff/Obershelp with autojunk off — check both call sites: neither passes junk) or use a Go port and re-pin thresholds against transliterated tests.
7. End-to-end cross-validation: record real LLM responses (request/response fixtures), run Python aider and the Go port on identical inputs, diff the resulting file trees. This is the analog of the starlark-go CLI oracle.
