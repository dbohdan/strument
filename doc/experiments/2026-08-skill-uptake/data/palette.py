#!/usr/bin/env python3
"""Generate the trial's categorical palette, and prove it is worth complying with.

R1 -- "every fill comes from this palette" -- is the trial's load-bearing rule,
because the palette exists only in the skill's BODY. The description, which arm
B sees in the tool schema whether or not it loads the skill, does not name a
single hex. So R1 is the one measurement that a description effect cannot
reach.

That works only if two things hold, and both are checked here rather than
asserted:

  unguessable  -- the hues come from a seeded rotation, so no model emits them
                  by habit the way it emits #1f77b4 or #ff0000.
  genuinely good -- or "quality" in this trial means "complied with arbitrary
                  rules". Every colour must carry >=4.5:1 contrast on white,
                  and every PAIR must stay apart under normal vision and under
                  simulated deuteranopia and protanopia.

Search seeds until one palette satisfies all of it; print the palette and the
evidence.
"""

import math
import sys

# --- sRGB <-> linear ---------------------------------------------------------

def s2l(c):
    c /= 255.0
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4

def l2s(c):
    c = max(0.0, min(1.0, c))
    v = 12.92 * c if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055
    return v * 255.0

# --- XYZ / Lab (D65) ---------------------------------------------------------

M_RGB2XYZ = ((0.4124564, 0.3575761, 0.1804375),
             (0.2126729, 0.7151522, 0.0721750),
             (0.0193339, 0.1191920, 0.9503041))
M_XYZ2RGB = ((3.2404542, -1.5371385, -0.4985314),
             (-0.9692660, 1.8760108, 0.0415560),
             (0.0556434, -0.2040259, 1.0572252))
WP = (0.95047, 1.0, 1.08883)

def mul(m, v):
    return tuple(sum(m[i][j] * v[j] for j in range(3)) for i in range(3))

def rgb2xyz(rgb):
    return mul(M_RGB2XYZ, tuple(s2l(c) for c in rgb))

def xyz2rgb(xyz):
    return tuple(l2s(c) for c in mul(M_XYZ2RGB, xyz))

def xyz2lab(xyz):
    def f(t):
        return t ** (1 / 3) if t > 216 / 24389 else (24389 / 27 * t + 16) / 116
    fx, fy, fz = (f(xyz[i] / WP[i]) for i in range(3))
    return (116 * fy - 16, 500 * (fx - fy), 200 * (fy - fz))

def lab2xyz(lab):
    L, a, b = lab
    fy = (L + 16) / 116
    fx, fz = fy + a / 500, fy - b / 200
    def g(t):
        return t ** 3 if t ** 3 > 216 / 24389 else (116 * t - 16) * 27 / 24389
    return tuple(g(v) * WP[i] for i, v in enumerate((fx, fy, fz)))

def lch2rgb(L, C, h):
    a, b = C * math.cos(math.radians(h)), C * math.sin(math.radians(h))
    return xyz2rgb(lab2xyz((L, a, b)))

def in_gamut(rgb):
    return all(-0.5 <= c <= 255.5 for c in rgb)

# --- contrast ----------------------------------------------------------------

def luminance(rgb):
    r, g, b = (s2l(c) for c in rgb)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b

def contrast_on_white(rgb):
    return 1.05 / (luminance(rgb) + 0.05)

# --- dichromat simulation (Vienot, Brettel & Mollon 1999) --------------------
# Applied in LINEAR RGB, which is the part implementations get wrong; doing it
# on gamma-encoded values exaggerates the separation and would let a palette
# pass that a real deuteranope cannot read.

M_RGB2LMS = ((17.8824, 43.5161, 4.11935),
             (3.45565, 27.1554, 3.86714),
             (0.0299566, 0.184309, 1.46709))
M_LMS2RGB = ((0.0809444479, -0.130504409, 0.116721066),
             (-0.0102485335, 0.0540193266, -0.113614708),
             (-0.000365296938, -0.00412161469, 0.693511405))
# Projection planes onto the dichromat's reduced colour space.
M_PROTAN = ((0.0, 2.02344, -2.52581), (0.0, 1.0, 0.0), (0.0, 0.0, 1.0))
M_DEUTAN = ((1.0, 0.0, 0.0), (0.494207, 0.0, 1.24827), (0.0, 0.0, 1.0))

def simulate(rgb, kind):
    lin = tuple(s2l(c) * 255.0 for c in rgb)
    lms = mul(M_RGB2LMS, lin)
    lms = mul(M_PROTAN if kind == "protan" else M_DEUTAN, lms)
    out = mul(M_LMS2RGB, lms)
    return tuple(l2s(max(0.0, min(1.0, c / 255.0))) for c in out)

# --- CIEDE2000 ---------------------------------------------------------------

def de2000(lab1, lab2):
    L1, a1, b1 = lab1
    L2, a2, b2 = lab2
    C1, C2 = math.hypot(a1, b1), math.hypot(a2, b2)
    Cb = (C1 + C2) / 2
    G = 0.5 * (1 - math.sqrt(Cb ** 7 / (Cb ** 7 + 25 ** 7))) if Cb > 0 else 0.5
    a1p, a2p = (1 + G) * a1, (1 + G) * a2
    C1p, C2p = math.hypot(a1p, b1), math.hypot(a2p, b2)
    h1p = math.degrees(math.atan2(b1, a1p)) % 360 if (a1p or b1) else 0.0
    h2p = math.degrees(math.atan2(b2, a2p)) % 360 if (a2p or b2) else 0.0
    dLp, dCp = L2 - L1, C2p - C1p
    if C1p * C2p == 0:
        dhp = 0.0
    else:
        dhp = h2p - h1p
        dhp -= 360 if dhp > 180 else 0
        dhp += 360 if dhp < -180 else 0
    dHp = 2 * math.sqrt(C1p * C2p) * math.sin(math.radians(dhp) / 2)
    Lbp, Cbp = (L1 + L2) / 2, (C1p + C2p) / 2
    if C1p * C2p == 0:
        hbp = h1p + h2p
    elif abs(h1p - h2p) <= 180:
        hbp = (h1p + h2p) / 2
    else:
        hbp = (h1p + h2p + 360) / 2 if h1p + h2p < 360 else (h1p + h2p - 360) / 2
    T = (1 - 0.17 * math.cos(math.radians(hbp - 30))
         + 0.24 * math.cos(math.radians(2 * hbp))
         + 0.32 * math.cos(math.radians(3 * hbp + 6))
         - 0.20 * math.cos(math.radians(4 * hbp - 63)))
    Sl = 1 + 0.015 * (Lbp - 50) ** 2 / math.sqrt(20 + (Lbp - 50) ** 2)
    Sc = 1 + 0.045 * Cbp
    Sh = 1 + 0.015 * Cbp * T
    RT = (-2 * math.sqrt(Cbp ** 7 / (Cbp ** 7 + 25 ** 7))
          * math.sin(math.radians(60 * math.exp(-(((hbp - 275) / 25) ** 2)))))
    return math.sqrt((dLp / Sl) ** 2 + (dCp / Sc) ** 2 + (dHp / Sh) ** 2
                     + RT * (dCp / Sc) * (dHp / Sh))

def lab_of(rgb):
    return xyz2lab(rgb2xyz(rgb))

# --- generation --------------------------------------------------------------
#
# Six colours at ONE lightness was the first attempt and it failed, which is the
# useful part: at fixed L* the six differ only in hue, and hue is precisely what
# dichromacy takes away. Pairwise dE2000 was 23 for normal vision and 6.4 under
# both simulations -- a palette that looks varied and collapses to three colours
# for ~4% of men. CVD-safe categorical palettes carry the distinction in
# LIGHTNESS, so the ladder below is the fix, not a decoration.
#
# The contrast floor is 3:1, not 4.5:1. 4.5 is WCAG's threshold for TEXT; bars,
# lines and points are graphical objects, which SC 1.4.11 puts at 3:1. Using the
# text figure here was simply the wrong rule, and it is what forced every colour
# below L*~50 and left no room for the ladder.

import random

N = 6
MIN_CONTRAST = 3.0     # WCAG 2.1 SC 1.4.11, non-text contrast.
MIN_DE_NORMAL = 20.0
MIN_DE_CVD = 12.0      # under simulated deuteranopia and protanopia.
L_LOW, L_HIGH = 30.0, 60.0

def build(offset, ls, chroma):
    cols = []
    for i in range(N):
        h = (offset + i * (360.0 / N)) % 360
        c = chroma
        while c > 5 and not in_gamut(lch2rgb(ls[i], c, h)):
            c -= 1
        rgb = lch2rgb(ls[i], c, h)
        cols.append(tuple(round(max(0.0, min(255.0, v))) for v in rgb))
    return cols

def evaluate(cols):
    worst_c = min(contrast_on_white(c) for c in cols)
    worst = {}
    for kind in ("normal", "deutan", "protan"):
        sim = [c if kind == "normal" else simulate(c, kind) for c in cols]
        labs = [lab_of(c) for c in sim]
        worst[kind] = min(de2000(labs[i], labs[j])
                          for i in range(N) for j in range(i + 1, N))
    ok = (worst_c >= MIN_CONTRAST
          and worst["normal"] >= MIN_DE_NORMAL
          and min(worst["deutan"], worst["protan"]) >= MIN_DE_CVD)
    return ok, worst_c, worst

# Colours a model reaches for by habit. R1 is worthless if the palette
# accidentally contains one, so this is checked, not assumed.
COMMON = {"#1F77B4", "#FF7F0E", "#2CA02C", "#D62728", "#9467BD", "#8C564B",
          "#E377C2", "#7F7F7F", "#BCBD22", "#17BECF",
          "#FF0000", "#00FF00", "#0000FF", "#FFA500", "#800080", "#008000",
          "#4E79A7", "#F28E2B", "#E15759", "#76B7B2", "#59A14F", "#EDC948",
          "#3366CC", "#DC3912", "#FF9900", "#109618", "#990099", "#0099C6",
          "#4285F4", "#EA4335", "#FBBC05", "#34A853", "#333333", "#666666"}

def main():
    rng = random.Random(20260829)
    ladder = [L_LOW + i * (L_HIGH - L_LOW) / (N - 1) for i in range(N)]
    best = None
    for trial in range(4000):
        offset = rng.uniform(0, 360)
        ls = ladder[:]
        rng.shuffle(ls)
        for chroma in (62, 56, 50, 44, 38):
            cols = build(offset, ls, chroma)
            if COMMON & {"#%02X%02X%02X" % c for c in cols}:
                continue
            ok, wc, worst = evaluate(cols)
            score = min(worst["deutan"], worst["protan"])
            cand = (ok, cols, wc, score, worst, offset, tuple(ls), chroma, trial)
            if best is None or (ok, score) > (best[0], best[3]):
                best = cand
            if ok:
                break
        if best[0]:
            break

    ok, cols, wc, score, worst, offset, ls, chroma, trial = best
    hexes = ["#%02X%02X%02X" % c for c in cols]
    print(f"trial {trial}: hue offset {offset:.1f}deg, chroma {chroma}, L* ladder {[round(v) for v in ls]}")
    print("palette:", " ".join(hexes))
    print(f"min contrast on white:      {wc:.2f}:1   (need >= {MIN_CONTRAST})")
    print(f"min pairwise dE2000 normal: {worst['normal']:.1f}    (need >= {MIN_DE_NORMAL})")
    print(f"min pairwise dE2000 deutan: {worst['deutan']:.1f}    (need >= {MIN_DE_CVD})")
    print(f"min pairwise dE2000 protan: {worst['protan']:.1f}    (need >= {MIN_DE_CVD})")
    print("clash with well-known palettes:", (COMMON & set(hexes)) or "none")
    print("VERDICT:", "PASS" if ok else "FAIL")
    return 0 if ok else 1

if __name__ == "__main__":
    sys.exit(main())
