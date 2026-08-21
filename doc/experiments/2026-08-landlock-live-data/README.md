# Raw output

`vps-2026-08-21.txt` is the trial's output verbatim, from

    OPENROUTER_API_KEY=… python3 script/sandbox-trial.py

on a Debian VPS with Landlock ABI 8, against Strument at `2c02d11`.

The one `FAIL` in it is the scorer defect described in the write-up, not a
sandbox failure; `script/sandbox-trial.py` has since been corrected.

`vps-2026-08-21-arms345.txt` is the second run, at `f7bdbf6`, adding the /run
probes, the worktree, and "a" = all this turn. All 37 checks green; the
`/sandbox` over-reporting described in the write-up is visible in arm 3's
transcript, between the path list and the build that was then refused.
