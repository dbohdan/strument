#!/bin/sh
# opencode Go live pass — drives the real strument binary through all three
# opencode adapters and reports what actually happened on disk.
#
#   OPENCODE_API_KEY=... ./opencode-live-pass.sh [/path/to/strument]
#   KEEP=1 ...                                     keep the logs
#
# Everything it writes goes under a temporary directory it prints and removes
# at the end; nothing touches your config, your repos or your history, and the
# key is never written to a file.
#
# Cases are scored from the *files on disk*, not from what the model said about
# them. A model that reports success having changed nothing reads identically
# to a real success in prose, and that is the failure worth catching.
#
# The model is *not* granted the shell: --no-shell withholds the bash tool
# rather than offering it and refusing the calls, so no step is spent on a
# capability that was never available. Granting it would be worse than noisy —
# a model that fixes a file with sed passes the check without ever exercising
# the edit tool, which is the failure this script exists to catch.
#
# stdin is /dev/null as well, so any *other* confirmation prompt reads EOF and
# declines rather than waiting for a keypress nobody is there to give, and a
# timeout backs that up so no case can wedge the run.
#
# STRUMENT_LIVE_CONFIG=path points it at a different config — the same nine
# cases against OpenRouter's openai/anthropic/responses endpoints, say, which
# is how to re-verify a dialect after changing one.
#
# The scorer is itself checked first, offline and for free, in both directions:
# it must accept a correctly fixed file and reject an untouched one. An earlier
# draft of this script had a scorer that could recognise a no-op but not a
# success, so every real fix reported FAIL — a rig that only fails one way is
# half a rig.
set -u

STRUMENT=${1:-strument}
if ! command -v "$STRUMENT" >/dev/null 2>&1 && [ ! -x "$STRUMENT" ]; then
  echo "cannot find strument: pass its path as the first argument" >&2; exit 2
fi
[ -n "${OPENCODE_API_KEY:-}" ] || { echo "set OPENCODE_API_KEY" >&2; exit 2; }

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/strument-oc-live.XXXXXX") || exit 2
cleanup() { [ "${KEEP:-}" = 1 ] && { echo "kept: $ROOT"; return; }; rm -rf "$ROOT"; }
trap cleanup EXIT INT TERM
mkdir -p "$ROOT/cfg/strument" "$ROOT/work" "$ROOT/logs"

# ---------------------------------------------------------------- fixtures --
write_broken() {
  cat > "$1/alpha.go" <<'GO'
package main

// Add returns the sum. It is wrong by one.
func Add(a, b int) int { return a + b + 1 }
GO
  cat > "$1/beta.go" <<'GO'
package main

// Mul returns the product. It is wrong.
func Mul(a, b int) int { return a * b + 1 }
GO
}

write_fixed() {
  cat > "$1/alpha.go" <<'GO'
package main

// Add returns the sum.
func Add(a, b int) int { return a + b }
GO
  cat > "$1/beta.go" <<'GO'
package main

// Mul returns the product.
func Mul(a, b int) int { return a * b }
GO
}

# ------------------------------------------------------------------ checks --
# Written to survive reformatting: a model may expand the body onto several
# lines, so these ask whether the bug is gone and the operation is still there,
# not whether a particular line matches.
alpha_fixed() { ! grep -q 'a + b + 1' alpha.go && grep -q 'a + b' alpha.go; }
beta_fixed()  { ! grep -q 'a \* b + 1' beta.go  && grep -q 'a \* b'  beta.go; }
both_fixed()  { alpha_fixed && beta_fixed; }
untouched()   { grep -q 'a + b + 1' alpha.go && grep -q 'a \* b + 1' beta.go; }

# ------------------------------------------------------- scorer self-test ---
selftest_fail=0
expect() { # description, expected(0|1), function
  ( eval "$3" ); got=$?
  [ "$got" -eq "$2" ] || { printf '  SCORER BROKEN: %s (got %s, want %s)\n' "$1" "$got" "$2"
                           selftest_fail=1; }
}
st="$ROOT/selftest"; mkdir -p "$st"
echo "=== scorer self-test (offline, no requests) ==="
write_broken "$st"
cd "$st" || exit 2
expect "untouched() accepts the original files"     0 untouched
expect "alpha_fixed() rejects the original file"    1 alpha_fixed
expect "both_fixed() rejects the original files"    1 both_fixed
write_fixed "$st"
expect "alpha_fixed() accepts a correct fix"        0 alpha_fixed
expect "beta_fixed() accepts a correct fix"         0 beta_fixed
expect "both_fixed() accepts correct fixes"         0 both_fixed
expect "untouched() rejects fixed files"            1 untouched
cd - >/dev/null || exit 2
if [ "$selftest_fail" -ne 0 ]; then
  echo "  the scorer cannot tell success from failure — refusing to spend." >&2
  exit 2
fi
echo "  scorer reads both directions."
echo

# ------------------------------------------------------------------ config --
# One model per adapter, from opencode's endpoint table. MiMo is the house
# default: cheapest, fastest, already known to work.
if [ -n "${STRUMENT_LIVE_CONFIG:-}" ]; then
  cp "$STRUMENT_LIVE_CONFIG" "$ROOT/cfg/strument/config.star" || exit 2
  echo "config: $STRUMENT_LIVE_CONFIG"
else
cat > "$ROOT/cfg/strument/config.star" <<'CFG'
key     = env("OPENCODE_API_KEY")
oc      = provider("opencode",           api_key = key)
oc_msg  = provider("opencode-anthropic", api_key = key)
oc_resp = provider("opencode-responses", api_key = key)

models = {
    "chat":      model(oc,      "mimo-v2.5",     context = 1050000, max_output = 8192, cache = True),
    "messages":  model(oc_msg,  "qwen3.8-flash", context = 262144,  max_output = 8192, cache = True),
    "responses": model(oc_resp, "gpt-5.6-luna",  context = 272000,  max_output = 8192, cache = True,
                       reasoning = "medium"),
}
default = "chat"
sandbox = ""
CFG
fi

# -------------------------------------------------------------------- runs --
# A case must never be able to wedge the run. `timeout` is not everywhere, so
# its absence is a warning rather than a failure.
LIMIT=${STRUMENT_LIVE_TIMEOUT:-240}
if command -v timeout >/dev/null 2>&1; then
  RUN="timeout ${LIMIT}s"
else
  RUN=""
  echo "note: no timeout(1) — a wedged case will block instead of failing" >&2
fi

pass=0; fail=0
run_case() { # name, model, prompt, check-function
  name=$1; model=$2; prompt=$3; check=$4
  dir="$ROOT/work/$name"; log="$ROOT/logs/$name.log"
  rm -rf "$dir"; mkdir -p "$dir"; write_broken "$dir"

  printf '%-26s ' "$name"
  # --no-shell: the bash tool is absent, not refused. stdin from /dev/null so
  # any other prompt reads EOF and declines rather than waiting.
  ( cd "$dir" && XDG_CONFIG_HOME="$ROOT/cfg" $RUN "$STRUMENT" chat --model "$model" \
      -m "$prompt" --no-git --no-history --no-color --no-shell --yes steps < /dev/null ) > "$log" 2>&1
  status=$?
  [ "$status" -eq 124 ] && echo "    (timed out after ${LIMIT}s)" >> "$log"

  if ( cd "$dir" && $check ); then
    echo "PASS"; pass=$((pass+1))
  else
    echo "FAIL  (exit $status)"
    fail=$((fail+1))
    grep -m2 -iE 'HTTP [45][0-9][0-9]|request failed|error' "$log" | sed 's/^/    /'
    sed -n '$p' "$log" | sed 's/^/    last: /'
    echo "    log: $log"
  fi
}

echo "=== one edit, one file ==="
for m in chat messages responses; do
  run_case "$m-single-edit" "$m" \
    "Read alpha.go, then fix the off-by-one in Add." alpha_fixed
done

echo
echo "=== two files in one turn (parallel tool calls) ==="
for m in chat messages responses; do
  run_case "$m-two-files" "$m" \
    "Read alpha.go and beta.go, then fix the bug in each." both_fixed
done

echo
echo "=== a question with no edit: answer, do not write ==="
for m in chat messages responses; do
  run_case "$m-read-only" "$m" \
    "Read alpha.go and tell me in one sentence what Add returns. Change nothing." untouched
done

echo
echo "-------------------------------------------------------------"
printf 'pass %d   fail %d\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || { [ "${KEEP:-}" = 1 ] || echo "re-run with KEEP=1 to keep the logs"; }
[ "$fail" -eq 0 ]
