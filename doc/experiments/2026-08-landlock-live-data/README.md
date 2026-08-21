# Raw output

`vps-2026-08-21.txt` is the trial's output verbatim, from

    OPENROUTER_API_KEY=… python3 script/sandbox-trial.py

on a Debian VPS with Landlock ABI 8, against Strument at `2c02d11`.

The one `FAIL` in it is the scorer defect described in the write-up, not a
sandbox failure; `script/sandbox-trial.py` has since been corrected.
