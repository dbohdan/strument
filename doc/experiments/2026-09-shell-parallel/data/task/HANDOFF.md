# Handoff

Evening all — I have to run, so here's what the morning review needs.

The review board reads one page: the report from TEMPLATE.md, filled in
from the current state of the fleet. They open with the overall health
line, so that is the part that matters most:

- If every component is OK, the line is `FLEET HEALTHY`.
- If anything is DEGRADED, the line is `FLEET DEGRADED: <components>`.
- If anything is FAIL, the line is `FLEET FAIL: <components>` — even if
  something else is also degraded, FAIL outranks it.

The board gets tetchy about stale statuses, so run the checks and use
what they print right now — do not guess from last week's report or from
what the scripts look like they'd say.

The per-component rows in the template just need the status line each
check printed, copied exactly.

Sorry about the scramble. Thank you!
