#!/usr/bin/env python3
"""A second and third chart fixture, so the result is not about one file.

Both are working-but-ugly in the same five ways, because a fixture that cannot
contain the phenomenon cannot measure it. Different shapes on purpose: a line
chart over time and a four-series bar chart, so a rule that only happens to be
satisfiable on grouped bars shows up as such.
"""
import json
import sys

W, H = 720, 400
PAD_L, PAD_R, PAD_T, PAD_B = 60, 30, 40, 60
UGLY = ["#FF0000", "#00AA00", "#0000FF", "#FF00FF"]
GOOD = ["#A5415A", "#7A490D", "#648C43", "#00A49B"]
GRID, INK = "#D6D6D6", "#3A3A3A"
UNITS = {"latency": "Request latency (ms)", "storage": "Storage used (TB)"}

# IDEAL is set by main(); the drawing helpers branch on it so one generator
# emits both the fixture and the reference. The reference exists only to prove
# the scorer can read 5/5 on THIS shape -- without it, "the fixture scores 0"
# is equally consistent with a rule that is unsatisfiable on a line chart, and
# arm B would be capped by the fixture rather than by the model.
IDEAL = False


def frame(a, plot_w, plot_h):
    if not IDEAL:
        a(f'  <rect x="0" y="0" width="{W}" height="{H}" fill="#EFEFEF"/>')
        a(f'  <rect x="{PAD_L}" y="{PAD_T}" width="{plot_w}" height="{plot_h}" '
          f'fill="#FFFFFF" stroke="#000000" stroke-width="1"/>')
    for i in range(6):
        y = PAD_T + plot_h * i / 5
        a(f'  <line class="grid" x1="{PAD_L}" y1="{y:.1f}" x2="{PAD_L + plot_w}" '
          f'y2="{y:.1f}" stroke="{GRID if IDEAL else "#999999"}" stroke-width="1"/>')


def axes(a, plot_w, plot_h, top, cats, ylabel, title):
    for i in range(6):
        val, y = top * (5 - i) / 5, PAD_T + plot_h * i / 5
        a(f'  <text x="{PAD_L - 8}" y="{y + 4:.1f}" text-anchor="end" fill="#000000">{val:.0f}</text>')
    for ci, c in enumerate(cats):
        x = PAD_L + plot_w * (ci + 0.5) / len(cats)
        a(f'  <text x="{x:.1f}" y="{PAD_T + plot_h + 18}" text-anchor="middle" fill="#000000">{c}</text>')
    ink = INK if IDEAL else "#000000"
    a(f'  <line x1="{PAD_L}" y1="{PAD_T + plot_h}" x2="{PAD_L + plot_w}" y2="{PAD_T + plot_h}" stroke="{ink}"/>')
    a(f'  <line x1="{PAD_L}" y1="{PAD_T}" x2="{PAD_L}" y2="{PAD_T + plot_h}" stroke="{ink}"/>')
    mid = PAD_T + plot_h / 2
    a(f'  <text x="16" y="{mid}" text-anchor="middle" fill="#000000" '
      f'transform="rotate(-90 16 {mid})">{ylabel}</text>')
    a(f'  <text x="{W / 2}" y="24" text-anchor="middle" font-size="15" fill="#000000">{title}</text>')


def legend(a, series, plot_w, tops=None):
    if IDEAL:
        # Rule 4: named beside their own marks, in their own colour.
        for si, s in enumerate(series):
            y = tops[si] if tops else PAD_T + 20 + si * 16
            a(f'  <text x="{PAD_L + plot_w + 6:.1f}" y="{y:.1f}" fill="{GOOD[si]}" '
              f'font-weight="bold">{s}</text>')
        return
    a('  <g class="legend">')
    for si, s in enumerate(series):
        ly = PAD_T + 10 + si * 18
        a(f'    <rect x="{PAD_L + plot_w - 100}" y="{ly}" width="12" height="12" fill="{(GOOD if IDEAL else UGLY)[si]}"/>')
        a(f'    <text x="{PAD_L + plot_w - 82}" y="{ly + 10}" fill="#000000">{s}</text>')
    a('  </g>')


def line_chart(title, ylabel, cats, series, data):
    plot_w, plot_h = W - PAD_L - PAD_R, H - PAD_T - PAD_B
    top = max(max(v) for v in data.values()) * 1.15
    o = []; a = o.append
    a(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
      f'font-family="Verdana, sans-serif" font-size="11">')
    frame(a, plot_w, plot_h)
    if not IDEAL:                                    # rule 5: verticals go
        for i in range(len(cats)):
            x = PAD_L + plot_w * i / (len(cats) - 1)
            a(f'  <line class="grid" x1="{x:.1f}" y1="{PAD_T}" x2="{x:.1f}" '
              f'y2="{PAD_T + plot_h}" stroke="#999999" stroke-width="1"/>')
    for si, s in enumerate(series):
        pts = []
        for ci, v in enumerate(data[s]):
            x = PAD_L + plot_w * ci / (len(cats) - 1)
            y = PAD_T + plot_h - plot_h * v / top
            pts.append(f"{x:.1f},{y:.1f}")
        a(f'  <polyline data-series="{s}" fill="none" stroke="{(GOOD if IDEAL else UGLY)[si]}" stroke-width="2" '
          f'points="{" ".join(pts)}"/>')
        for ci, v in enumerate(data[s]):
            x = PAD_L + plot_w * ci / (len(cats) - 1)
            y = PAD_T + plot_h - plot_h * v / top
            a(f'  <circle data-series="{s}" data-cat="{cats[ci]}" data-value="{v}" '
              f'cx="{x:.1f}" cy="{y:.1f}" r="3" fill="{(GOOD if IDEAL else UGLY)[si]}"/>')
    axes(a, plot_w, plot_h, top, cats, ylabel, title)
    legend(a, series, plot_w,
           [PAD_T + plot_h - plot_h * data[s][-1] / top + 4 for s in series])
    a('</svg>')
    return "\n".join(o)


def bar_chart(title, ylabel, cats, series, data):
    plot_w, plot_h = W - PAD_L - PAD_R, H - PAD_T - PAD_B
    top = max(max(v) for v in data.values()) * 1.1
    gw = plot_w / len(cats); bw = gw / (len(series) + 1)
    o = []; a = o.append
    a(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
      f'font-family="Verdana, sans-serif" font-size="11">')
    frame(a, plot_w, plot_h)
    if not IDEAL:
        for i in range(len(cats) + 1):
            x = PAD_L + plot_w * i / len(cats)
            a(f'  <line class="grid" x1="{x:.1f}" y1="{PAD_T}" x2="{x:.1f}" '
              f'y2="{PAD_T + plot_h}" stroke="#999999" stroke-width="1"/>')
    for ci, cat in enumerate(cats):
        for si, s in enumerate(series):
            v = data[s][ci]; h = plot_h * v / top
            x = PAD_L + ci * gw + (si + 0.5) * bw
            a(f'  <rect data-cat="{cat}" data-series="{s}" data-value="{v}" x="{x:.1f}" '
              f'y="{PAD_T + plot_h - h:.1f}" width="{bw:.1f}" height="{h:.1f}" fill="{(GOOD if IDEAL else UGLY)[si]}"/>')
    axes(a, plot_w, plot_h, top, cats, ylabel, title)
    legend(a, series, plot_w,
           [PAD_T + plot_h - plot_h * data[s][-1] / top + 4 for s in series])
    a('</svg>')
    return "\n".join(o)


SPECS = {
    "latency": dict(
        kind="line", title="Request latency by week", ylabel="Value",
        cats=["W1", "W2", "W3", "W4", "W5", "W6", "W7", "W8"],
        data={"p50": [42, 45, 41, 48, 44, 47, 43, 46],
              "p95": [180, 195, 172, 210, 188, 205, 179, 198],
              "p99": [420, 455, 401, 490, 437, 478, 415, 462]}),
    "storage": dict(
        kind="bar", title="Storage used by tier", ylabel="Amount",
        cats=["Jan", "Feb", "Mar", "Apr", "May"],
        data={"hot": [120, 135, 128, 149, 156],
              "warm": [340, 358, 371, 389, 402],
              "cold": [890, 912, 947, 981, 1024],
              "archive": [210, 214, 219, 223, 228]}),
}


def main():
    global IDEAL
    name, out = sys.argv[1], sys.argv[2]
    IDEAL = "--ideal" in sys.argv
    spec = SPECS[name]
    if IDEAL:
        spec = dict(spec, ylabel=UNITS[name])
    series = list(spec["data"])
    fn = line_chart if spec["kind"] == "line" else bar_chart
    svg = fn(spec["title"], spec["ylabel"], spec["cats"], series, spec["data"])
    open(out, "w").write(
        f'<!DOCTYPE html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n'
        f'<title>{spec["title"]}</title>\n</head>\n<body>\n{svg}\n</body>\n</html>\n')
    vals = sorted(v for s in series for v in spec["data"][s])
    print(json.dumps({"values": vals, "bars": len(vals), "series": series,
                      "title": spec["title"]}))


if __name__ == "__main__":
    main()
