# How the panel keeps secrets from the model

**Status: survey, 2026-11-27.** Strument keeps credentials from the model in
four ways: a list of secret-shaped files its tools refuse, a parse of shell
commands that sends one naming such a file to the user, a rule that a project
config can add to the list but not shorten it, and an allowlist for the
environment of the commands the model causes. The panel was read for each, in
source, at the same commits as
[the facts survey](../2026-11-facts-survey/) of the same day:
OpenCode `36bfb85`, Pi `2126569`, Codex `8cd512c`, Kimi Code `63338e9`,
DeepSeek Harness `4b399c39`. It updates the October survey behind
`internal/secretfile`, which found Kimi Code asking before reading its list;
it now refuses.

## The files

| | list | what happens | tools it covers | shell commands | a file's name |
| --- | --- | --- | --- | --- | --- |
| OpenCode | `*.env`, `*.env.*`; `*.env.example` allowed | asks | `read`; `edit`'s did-you-mean hint is withheld unless reading is allowed | its parser extracts directories, for the outside-the-project rule, not files | shown |
| Pi | none built in; an example extension blocks *writes* to `.env`, `.git/`, `node_modules/` | — | — | — | shown |
| Codex | none for reads; `.git`, `.agents`, `.codex` and `.aws` are read-only inside writable roots | write denied | writes | — | shown |
| Kimi Code | `.env`, `.env.*` except `.example`/`.sample`/`.template`; `id_rsa`, `id_ed25519`, `id_ecdsa` and their prefixes, but not `.pub`; any file named `credentials`; `.aws/credentials`, `.gcp/credentials`; each with `.bak`, `.backup`, `.copy`, `.disabled`, `.key`, `.old`, `.orig`, `.pem`, `.save`, `.tmp` variants | refuses: "Access is blocked to protect secrets" | `read` and `write`; `glob` and `grep` drop the files from their results and say how many | an instruction only: "Bash enforces none of these guards" | hidden, and counted |
| DeepSeek Harness | none found | — | — | — | shown |
| Strument | Kimi's October defaults plus `.envrc`, `.netrc`, `.pgpass`, `.git-credentials`, `.npmrc`, `.pypirc`, `.vault-token`, cloud and container configs, `.gnupg/`, `.password-store/`, keyrings | refuses | `read`, `grep`, `edit` | parsed, words expanded as the shell would; a hit goes to the user, past `approve_model` | shown |

Kimi Code's list is fixed in code. In Strument the user exempts a file, and a
project config can add patterns but not exempt any. OpenCode's rules are
permissions the user edits like any other.

Two ideas are shared without either project citing the other. OpenCode
withholds `edit`'s closest-lines hint for a file the model may not read,
which is Strument's "an edit is a read in disguise". And where a shell deny
rule exists, OpenCode refuses a command its scanner cannot analyze (`eval`
and the like) and asks for a rewrite; Strument leaves such a word
unresolved, so the command goes through the ordinary approval.

## The environment of a command

| | default | how |
| --- | --- | --- |
| OpenCode | everything | `process.env`, or the session's environment |
| Pi | everything | `process.env`, with its bin directory on `PATH` |
| Codex | everything | `shell_environment_policy`: `inherit = all`, and `ignore_default_excludes` defaults to true, which skips its `*KEY*`, `*SECRET*`, `*TOKEN*` exclusion. Opt-in. |
| Kimi Code | everything | `process.env` with a few overrides |
| DeepSeek Harness | everything except credential-shaped names | `/KEY\|PASSWORD\|SECRET\|TOKEN/i` dropped for every child, on by default; an explicit entry is added back after |
| Strument | an allowlist | `internal/envallow`; `env_allow` adds names |

Four of five pass the model's commands every API key in the user's
environment. DeepSeek Harness denies by name shape. Strument alone allows by
name: credential-shaped names are left out by omission, and a variable a
workflow needs is added with `env_allow` as a visible decision.

## What it says for Strument

- **The backup variants are a gap.** Strument's patterns miss `id_rsa.bak`,
  `id_ed25519.orig` and `id_rsa.pem` outside `~/.ssh`, `.aws/credentials.bak`,
  `.netrc.old` and `.npmrc.bak`, all of which Kimi Code's variant suffixes
  would catch. Its `.env.*` and `.ssh/id_*` already cover those two cases.
- **Hiding names is a choice Strument made the other way.** Kimi Code drops
  secret files from `glob` and `grep` results. Strument lists them, on the
  grounds that a name gives nothing away.
- **Nobody else checks shell commands for secret files.** Kimi Code says so
  in its prompt and leaves it to the model.
- **The environment allowlist has no equal in the panel.** The nearest is
  DeepSeek Harness's shape filter.
