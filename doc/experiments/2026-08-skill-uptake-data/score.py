#!/usr/bin/env python3
"""Score one run's chart.html against the five house rules.

Reads the FILE ON DISK, never the transcript. That is deliberate: every scorer
fault recorded in doc/experimenting.md 1, 15 and 17 came from parsing Strument's
rendered output and mistaking reasoning for an answer or a marker for a
delimiter. The artifact has no such ambiguity -- the model either changed the
file or it did not.

Each check corresponds 1:1 to a numbered rule in the skill. A first draft had a
sixth check ("meaning is not encoded in red versus green") that had to be cut:
the generated palette itself contains a reddish #A5415A and a greenish #648C43,
so a chart obeying rule 1 perfectly would have failed it. A check the correct
answer cannot pass is worse than no check.
"""

import json
import re
import sys
import xml.dom.minidom

PALETTE = ["#A5415A", "#7A490D", "#648C43", "#00A49B", "#007DB7", "#54397D"]
PALETTE_SET = {c.upper() for c in PALETTE}

CSS_NAMES = {
    "red": "#FF0000", "green": "#008000", "lime": "#00FF00", "blue": "#0000FF",
    "orange": "#FFA500", "purple": "#800080", "gray": "#808080",
    "grey": "#808080", "black": "#000000", "white": "#FFFFFF",
    "steelblue": "#4682B4", "tomato": "#FF6347", "teal": "#008080",
    "crimson": "#DC143C", "darkgreen": "#006400", "navy": "#000080",
    "gold": "#FFD700", "silver": "#C0C0C0", "none": None,
    "transparent": None, "currentcolor": None,
}


def norm_color(raw):
    """Return an upper-case #RRGGBB, or None for 'not a concrete colour'."""
    s = raw.strip().lower()
    if s in CSS_NAMES:
        return CSS_NAMES[s]
    m = re.fullmatch(r"#([0-9a-f]{3})", s)
    if m:
        return "#" + "".join(c * 2 for c in m.group(1)).upper()
    m = re.fullmatch(r"#([0-9a-f]{6})", s)
    if m:
        return "#" + m.group(1).upper()
    m = re.fullmatch(r"rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:[\s,/]+[\d.%]+)?\s*\)", s)
    if m:
        return "#%02X%02X%02X" % tuple(int(round(float(g))) for g in m.groups())
    return None


def is_neutral(hexcol):
    """Greys, black and white are structural, not data -- rule 1 says so.

    Measured as Lab chroma, not as max(rgb)-min(rgb). The channel-spread test
    called Tailwind's cool greys (#374151, #4B5563, #9AA3AD) data colours,
    because a deliberately blue-tinted grey has a spread of 26 while being, to
    the eye, grey. One model used exactly those for its axes and text -- which
    rule 1 explicitly permits -- and R1 marked it down for obeying the rule.
    The same class of fault as the axis-versus-gridline confusion in R5.

    Chroma separates the two cleanly on the observed colours: structural greys
    run C* 0.0-11.0, data colours 17.3 (ColorBrewer's palest blue) upward.
    """
    r, g, b = (int(hexcol[i:i + 2], 16) for i in (1, 3, 5))

    def lin(c):
        c /= 255.0
        return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4

    rl, gl, bl = lin(r), lin(g), lin(b)
    x = (0.4124564 * rl + 0.3575761 * gl + 0.1804375 * bl) / 0.95047
    y = (0.2126729 * rl + 0.7151522 * gl + 0.0721750 * bl)
    z = (0.0193339 * rl + 0.1191920 * gl + 0.9503041 * bl) / 1.08883

    def f(t):
        return t ** (1 / 3) if t > 216 / 24389 else (24389 / 27 * t + 16) / 116

    fx, fy, fz = f(x), f(y), f(z)
    a_, b_ = 500 * (fx - fy), 200 * (fy - fz)
    return (a_ * a_ + b_ * b_) ** 0.5 < 14.0


def colors_in(text):
    """Every concrete colour the document names, from attributes and CSS."""
    out = []
    for m in re.finditer(r'(?:fill|stroke|color|background(?:-color)?)\s*[=:]\s*"?\'?([^;"\'>\s]+)', text, re.I):
        c = norm_color(m.group(1))
        if c:
            out.append(c)
    for m in re.finditer(r"#[0-9a-fA-F]{6}\b|#[0-9a-fA-F]{3}\b", text):
        c = norm_color(m.group(0))
        if c:
            out.append(c)
    return out


def svg_of(text):
    i, j = text.find("<svg"), text.rfind("</svg>")
    return text[i:j + 6] if i >= 0 and j > i else ""


# --- the five rules ---------------------------------------------------------

def r1_palette(text):
    """Data colours come from the palette, and at least two are used."""
    data = {c for c in colors_in(text) if not is_neutral(c)}
    used = data & PALETTE_SET
    return len(used) >= 2 and data <= PALETTE_SET, {
        "palette_used": sorted(used), "off_palette": sorted(data - PALETTE_SET)}


def r2_chartjunk(text):
    """No full-bleed background fill, and no border around the plot area."""
    svg = svg_of(text)
    try:
        w = float(re.search(r'<svg[^>]*\swidth="([\d.]+)"', svg).group(1))
        h = float(re.search(r'<svg[^>]*\sheight="([\d.]+)"', svg).group(1))
    except (AttributeError, ValueError):
        return False, {"why": "no svg width/height"}
    bg, border = [], []
    for m in re.finditer(r"<rect\b[^>]*>", svg):
        tag = m.group(0)
        gf = lambda n: (lambda x: float(x.group(1)) if x else None)(
            re.search(rf'\s{n}="([\d.-]+)"', tag))
        rw, rh = gf("width"), gf("height")
        if rw is None or rh is None:
            continue
        fill = norm_color((re.search(r'fill="([^"]*)"', tag) or [None, "none"])[1]
                          if re.search(r'fill="([^"]*)"', tag) else "none")
        stroke = re.search(r'stroke="([^"]*)"', tag)
        stroke = norm_color(stroke.group(1)) if stroke else None
        big = rw >= 0.55 * w and rh >= 0.55 * h
        if big and fill is not None and fill != "#FFFFFF":
            bg.append(tag[:60])
        if big and stroke is not None:
            border.append(tag[:60])
    # A CSS background on body/svg counts as a background fill too.
    css_bg = re.search(r"(?:body|svg|\.chart)[^{}]*\{[^{}]*background[^{}]*\}", text, re.I)
    if css_bg and not re.search(r"background[^;{}]*:\s*(?:#fff(?:fff)?|white|none|transparent)",
                                css_bg.group(0), re.I):
        bg.append("css:" + css_bg.group(0)[:40])

    # Rule 2's third clause: "no drop shadows, bevels, gradients or 3-D
    # effects". Checking only the background and the border read one model's
    # gradient-filled, drop-shadowed bars as compliant -- the metric was one
    # clause shorter than the rule it is named after, and it inflated the
    # BASELINE arm, which is the direction that matters.
    fx = []
    if re.search(r"<(?:linear|radial)Gradient\b", svg, re.I):
        if re.search(r'fill="url\(#', svg, re.I):
            fx.append("gradient fill")
    if re.search(r"<fe(?:DropShadow|GaussianBlur|SpecularLighting|DiffuseLighting)\b", svg, re.I):
        fx.append("filter effect")
    if re.search(r'\sfilter="url\(#[^"]*"[^>]*data-value|data-value[^>]*\sfilter="url\(#', svg, re.I):
        fx.append("filter on a data mark")
    return (not bg and not border and not fx,
            {"backgrounds": bg, "borders": border, "effects": fx})


UNIT_HINT = re.compile(
    r"\(\s*[^)]{1,40}\s*\)|\b(?:usd|eur|gbp|\$|%|ms|sec|seconds|minutes|hours|"
    r"days|weeks|thousand|thousands|million|millions|k\b|bytes|kb|mb|gb|"
    r"per\s+\w+|count|requests|users|sign-?ups|units)\b", re.I)

def r3_unit(text):
    """The value axis names a quantity AND a unit; 'Value' is the null case."""
    svg = svg_of(text)
    rotated = re.findall(r"<text[^>]*transform=\"[^\"]*rotate\([^)]*\)[^\"]*\"[^>]*>(.*?)</text>",
                         svg, re.S)
    rotated += re.findall(r"<text[^>]*writing-mode[^>]*>(.*?)</text>", svg, re.S)
    labels = [re.sub(r"<[^>]+>", "", t).strip() for t in rotated]
    labels = [t for t in labels if t]
    if not labels:
        return False, {"why": "no rotated axis label", "labels": labels}
    generic = {"value", "values", "amount", "amounts", "y", "number", "count"}
    ok = any(t.lower() not in generic and UNIT_HINT.search(t) for t in labels)
    return ok, {"labels": labels}


SERIES = ["2025", "2026"]   # overridden per fixture by score_file


def r4_direct_labels(text):
    """The legend is gone and the series names still appear as text."""
    svg = svg_of(text)
    has_legend = bool(re.search(r'class="[^"]*legend|id="[^"]*legend|<!--\s*legend', svg, re.I))
    texts = [re.sub(r"<[^>]+>", "", t).strip()
             for t in re.findall(r"<text[^>]*>(.*?)</text>", svg, re.S)]
    named = sum(1 for s in SERIES if any(s in t for t in texts))
    return (not has_legend) and named >= len(SERIES), {
        "legend": has_legend, "series_named": named, "of": len(SERIES)}


def r5_gridlines(text):
    """No vertical gridlines survive.

    Axes are classified by GEOMETRY, not by colour. The first version called any
    light-grey line a gridline, which misread a model that had deleted all seven
    verticals and greyed the y-axis from black to #BBBBBB -- exactly what rule 1
    asks for. R5 was then punishing a model for obeying rule 1, and the model's
    own summary was right where the scorer was wrong. A line lying on the left or
    right edge of the plot box is an axis or a frame; only INTERIOR verticals are
    gridlines.
    """
    svg = svg_of(text)
    cands = []
    for m in re.finditer(r"<line\b[^>]*>", svg):
        tag = m.group(0)
        g = lambda n: (lambda x: float(x.group(1)) if x else None)(
            re.search(rf'\s{n}="([\d.-]+)"', tag))
        x1, x2, y1, y2 = g("x1"), g("x2"), g("y1"), g("y2")
        if None in (x1, x2, y1, y2):
            continue
        stroke = re.search(r'stroke="([^"]*)"', tag)
        col = norm_color(stroke.group(1)) if stroke else None
        is_grid = bool(re.search(r'class="[^"]*grid', tag, re.I))
        if not is_grid and col is not None and is_neutral(col):
            is_grid = int(col[1:3], 16) >= 0x66
        if is_grid:
            cands.append((x1, y1, x2, y2))
    if not cands:
        return True, {"vertical": 0, "horizontal": 0, "note": "no grid candidates"}

    xs = [v for c in cands for v in (c[0], c[2])]
    ys = [v for c in cands for v in (c[1], c[3])]
    xmin, xmax, ymin, ymax = min(xs), max(xs), min(ys), max(ys)
    tol = max(1.0, (xmax - xmin) * 0.01)

    vert = horiz = axis = 0
    for x1, y1, x2, y2 in cands:
        if abs(x1 - x2) < 0.5 and abs(y1 - y2) > 1:
            if abs(x1 - xmin) <= tol or abs(x1 - xmax) <= tol:
                axis += 1          # y-axis, or a right-hand frame line
            else:
                vert += 1
        elif abs(y1 - y2) < 0.5 and abs(x1 - x2) > 1:
            horiz += 1
    for m in re.finditer(r'<path\b[^>]*\sd="([^"]*)"[^>]*>', svg):
        d = m.group(1)
        vert += len(re.findall(r"M\s*([\d.-]+)[\s,]+([\d.-]+)\s*[Vv]\s*([\d.-]+)", d))
        horiz += len(re.findall(r"M\s*([\d.-]+)[\s,]+([\d.-]+)\s*[Hh]\s*([\d.-]+)", d))
    return vert == 0, {"vertical": vert, "horizontal": horiz, "edge_lines": axis}


RULES = [("R1_palette", r1_palette), ("R2_chartjunk", r2_chartjunk),
         ("R3_unit", r3_unit), ("R4_direct_labels", r4_direct_labels),
         ("R5_gridlines", r5_gridlines)]


# --- counter-metrics --------------------------------------------------------

def counters(text, key):
    svg = svg_of(text)
    out = {}
    try:
        xml.dom.minidom.parseString(svg or "<svg/>")
        out["wellformed"] = bool(svg)
    except Exception as e:
        out["wellformed"] = False
        out["parse_error"] = str(e)[:120]

    vals = [float(v) for v in re.findall(r'data-value="([\d.]+)"', text)]
    if vals:
        out["data_attrs"] = "present"
        out["data_ok"] = sorted(round(v) for v in vals) == sorted(key["values"])
    else:
        # Counted separately, never folded into pass/fail: dropping the
        # attributes is not corrupting the data, and calling it that would be a
        # metric one clause wider than the phenomenon.
        out["data_attrs"] = "dropped"
        out["data_ok"] = None
    marks = len(re.findall(r"<rect\b", svg)) + len(re.findall(r"<(?:path|polyline)\b", svg))
    out["marks"] = marks
    out["enough_marks"] = marks >= key["bars"]
    return out


def score_file(path, key):
    global SERIES
    SERIES = key.get("series", ["2025", "2026"])
    text = open(path, encoding="utf-8", errors="replace").read()
    rules, detail = {}, {}
    for name, fn in RULES:
        ok, info = fn(text)
        rules[name] = bool(ok)
        detail[name] = info
    res = {"rules": rules, "n_rules": sum(rules.values()), "detail": detail}
    res.update(counters(text, key))
    return res


def main():
    key = json.load(open(sys.argv[2]))
    r = score_file(sys.argv[1], key)
    print(json.dumps(r, indent=1))
    return 0


if __name__ == "__main__":
    sys.exit(main())
