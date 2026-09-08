Please review `experimenting.md` as an independent editor.

**Audience:** a new contributor or model preparing to run a live experiment in this repository. They may know the codebase only partially and should be able to use the document under time pressure.

Please assess:

1. How easy is it to understand the handbook’s purpose and how to use it?
2. Can a reader quickly find the rules needed before launching a run?
3. Which sections, terms, or transitions are confusing without prior knowledge?
4. Does the document clearly distinguish model failures, provider failures, scorer failures, fixture/design failures, and runner failures?
5. Are the general rules sufficiently separated from Strument-specific implementation details?
6. Are any rules duplicated, contradictory, too strongly stated, or missing important qualifications?
7. Which three small structural or wording changes would improve it most?

Please do **not** rewrite the whole document. Return:

- an overall clarity rating from 1–10, with separate ratings for “reading as an essay” and “using as a pre-run reference”;
- the five highest-value improvements, ranked;
- concrete references to section numbers and quoted headings or phrases;
- proposed wording or structure only for the highest-priority changes;
- anything you think should remain unchanged because it is already effective.

Be alert to the difference between:

- a result being null,
- a treatment not being applied,
- a fixture not containing the phenomenon,
- a scorer misclassifying output,
- a provider or model producing no usable response,
- and the runner failing.

 Give your own assessment rather than trying to agree with an implied prior review.
