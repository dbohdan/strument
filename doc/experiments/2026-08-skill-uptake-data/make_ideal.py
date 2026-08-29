#!/usr/bin/env python3
"""The reference answer: the same chart obeying all five rules.

Its only job is to prove the scorer can read 5/5. Without it, "the fixture
scores 0" is consistent with a scorer that always returns 0 -- which is
doc/experimenting.md 17's whole subject.
"""
import sys

W, H = 720, 400
PAD_L, PAD_R, PAD_T, PAD_B = 78, 96, 48, 56
GOOD = {"2025": "#A5415A", "2026": "#007DB7"}   # two of the six, in order
GRID = "#D6D6D6"
INK = "#3A3A3A"


def main():
    cats = ["Q1", "Q2", "Q3", "Q4", "Q5", "Q6"]
    series = ["2025", "2026"]
    data = {"2025": [412, 388, 455, 501, 470, 523],
            "2026": [455, 470, 498, 560, 544, 611]}
    plot_w, plot_h = W - PAD_L - PAD_R, H - PAD_T - PAD_B
    top = max(max(v) for v in data.values()) * 1.1
    group_w = plot_w / len(cats)
    bar_w = group_w / (len(series) + 1)

    o = []
    a = o.append
    a(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
      f'viewBox="0 0 {W} {H}" font-family="Verdana, sans-serif" font-size="11">')
    # Rule 2: no background rect, no plot border. Rule 5: horizontals only.
    for i in range(6):
        y = PAD_T + plot_h * i / 5
        a(f'  <line class="grid" x1="{PAD_L}" y1="{y:.1f}" x2="{PAD_L + plot_w}" '
          f'y2="{y:.1f}" stroke="{GRID}" stroke-width="1"/>')
    for ci, cat in enumerate(cats):
        for si, s in enumerate(series):
            v = data[s][ci]
            h = plot_h * v / top
            x = PAD_L + ci * group_w + (si + 0.5) * bar_w
            y = PAD_T + plot_h - h
            a(f'  <rect data-cat="{cat}" data-series="{s}" data-value="{v}" '
              f'x="{x:.1f}" y="{y:.1f}" width="{bar_w:.1f}" height="{h:.1f}" '
              f'fill="{GOOD[s]}"/>')
    for i in range(6):
        val = top * (5 - i) / 5
        y = PAD_T + plot_h * i / 5
        a(f'  <text x="{PAD_L - 10}" y="{y + 4:.1f}" text-anchor="end" fill="{INK}">{val:.0f}</text>')
    for ci, cat in enumerate(cats):
        x = PAD_L + (ci + 0.5) * group_w
        a(f'  <text x="{x:.1f}" y="{PAD_T + plot_h + 18}" text-anchor="middle" fill="{INK}">{cat}</text>')
    # Rule 4: series named beside their own last bars, in their own colour.
    for si, s in enumerate(series):
        h = plot_h * data[s][-1] / top
        y = PAD_T + plot_h - h + 4
        a(f'  <text x="{PAD_L + plot_w + 8:.1f}" y="{y:.1f}" fill="{GOOD[s]}" '
          f'font-weight="bold">{s}</text>')
    # Rule 3: quantity and unit.
    ylab = "Revenue (thousands of USD)"
    a(f'  <text x="18" y="{PAD_T + plot_h / 2}" text-anchor="middle" fill="{INK}" '
      f'transform="rotate(-90 18 {PAD_T + plot_h / 2})">{ylab}</text>')
    a(f'  <text x="{PAD_L}" y="26" text-anchor="start" font-size="15" fill="{INK}">Quarterly revenue</text>')
    a('</svg>')
    svg = "\n".join(o)
    open(sys.argv[1], "w").write(
        f'<!DOCTYPE html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n'
        f'<title>Quarterly revenue</title>\n</head>\n<body>\n{svg}\n</body>\n</html>\n')


if __name__ == "__main__":
    main()
