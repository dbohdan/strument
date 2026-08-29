#!/usr/bin/env python3
"""Emit a working-but-ugly chart fixture.

Working, so that basic SVG competence cannot swamp the effect: every run starts
from a chart that already renders, and the task is only to improve it. Ugly in
exactly five ways, one per rule, so the fixture CONTAINS the phenomenon it is
scored on -- the failure doc/experimenting.md 18 calls "a hazard that does not
fire for the arm built to trip on it".

Each bar carries data-cat / data-series / data-value. That is what makes the
"did it keep the data" counter-metric robust to restyling: the check compares a
set of numbers, not pixel geometry, so changing the margins or the scale (both
legitimate) cannot look like data corruption.
"""

import json
import sys

W, H = 720, 400
PAD_L, PAD_R, PAD_T, PAD_B = 60, 30, 40, 60

# Deliberately the two colours rule 2 forbids, and neither is in the palette.
UGLY = {"2025": "#FF0000", "2026": "#00AA00"}


def bar_chart(title, ylabel, cats, series, data):
    plot_w = W - PAD_L - PAD_R
    plot_h = H - PAD_T - PAD_B
    vmax = max(max(v) for v in data.values())
    top = vmax * 1.1
    group_w = plot_w / len(cats)
    bar_w = group_w / (len(series) + 1)

    out = []
    a = out.append
    a(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
      f'viewBox="0 0 {W} {H}" font-family="Verdana, sans-serif" font-size="11">')
    # Rule 5 violations: a background fill, a plot border, and BOTH grids.
    a(f'  <rect x="0" y="0" width="{W}" height="{H}" fill="#EFEFEF"/>')
    a(f'  <rect x="{PAD_L}" y="{PAD_T}" width="{plot_w}" height="{plot_h}" '
      f'fill="#FFFFFF" stroke="#000000" stroke-width="1"/>')
    for i in range(6):
        y = PAD_T + plot_h * i / 5
        a(f'  <line class="grid" x1="{PAD_L}" y1="{y:.1f}" x2="{PAD_L + plot_w}" '
          f'y2="{y:.1f}" stroke="#999999" stroke-width="1"/>')
    for i in range(len(cats) + 1):
        x = PAD_L + plot_w * i / len(cats)
        a(f'  <line class="grid" x1="{x:.1f}" y1="{PAD_T}" x2="{x:.1f}" '
          f'y2="{PAD_T + plot_h}" stroke="#999999" stroke-width="1"/>')
    # Bars.
    for ci, cat in enumerate(cats):
        for si, s in enumerate(series):
            v = data[s][ci]
            h = plot_h * v / top
            x = PAD_L + ci * group_w + (si + 0.5) * bar_w
            y = PAD_T + plot_h - h
            a(f'  <rect data-cat="{cat}" data-series="{s}" data-value="{v}" '
              f'x="{x:.1f}" y="{y:.1f}" width="{bar_w:.1f}" height="{h:.1f}" '
              f'fill="{UGLY[s]}"/>')
    # Axes.
    a(f'  <line x1="{PAD_L}" y1="{PAD_T + plot_h}" x2="{PAD_L + plot_w}" '
      f'y2="{PAD_T + plot_h}" stroke="#000000" stroke-width="1"/>')
    a(f'  <line x1="{PAD_L}" y1="{PAD_T}" x2="{PAD_L}" y2="{PAD_T + plot_h}" '
      f'stroke="#000000" stroke-width="1"/>')
    for i in range(6):
        val = top * (5 - i) / 5
        y = PAD_T + plot_h * i / 5
        a(f'  <text x="{PAD_L - 8}" y="{y + 4:.1f}" text-anchor="end" '
          f'fill="#000000">{val:.0f}</text>')
    for ci, cat in enumerate(cats):
        x = PAD_L + (ci + 0.5) * group_w
        a(f'  <text x="{x:.1f}" y="{PAD_T + plot_h + 18}" text-anchor="middle" '
          f'fill="#000000">{cat}</text>')
    # Rule 3 violation: the value axis says "Value".
    a(f'  <text x="16" y="{PAD_T + plot_h / 2}" text-anchor="middle" '
      f'fill="#000000" transform="rotate(-90 16 {PAD_T + plot_h / 2})">{ylabel}</text>')
    a(f'  <text x="{W / 2}" y="24" text-anchor="middle" font-size="15" '
      f'fill="#000000">{title}</text>')
    # Rule 4 violation: a legend, for two series.
    a(f'  <g class="legend">')
    for si, s in enumerate(series):
        ly = PAD_T + 10 + si * 18
        a(f'    <rect x="{PAD_L + plot_w - 90}" y="{ly}" width="12" height="12" '
          f'fill="{UGLY[s]}"/>')
        a(f'    <text x="{PAD_L + plot_w - 72}" y="{ly + 10}" fill="#000000">{s}</text>')
    a(f'  </g>')
    a('</svg>')
    return "\n".join(out)


def main():
    cats = ["Q1", "Q2", "Q3", "Q4", "Q5", "Q6"]
    series = ["2025", "2026"]
    data = {"2025": [412, 388, 455, 501, 470, 523],
            "2026": [455, 470, 498, 560, 544, 611]}
    svg = bar_chart("Quarterly revenue", "Value", cats, series, data)
    html = f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Quarterly revenue</title>
</head>
<body>
{svg}
</body>
</html>
"""
    out = sys.argv[1]
    open(out, "w").write(html)
    # The answer key for the data counter-metric, written beside the fixture and
    # never inside the project the model sees.
    key = sorted(v for s in series for v in data[s])
    print(json.dumps({"values": key, "bars": len(key),
                      "unit_absent": "Value", "title": "Quarterly revenue"}))


if __name__ == "__main__":
    main()
