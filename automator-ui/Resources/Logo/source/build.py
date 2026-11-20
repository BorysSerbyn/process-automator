"""Round 2 deliverables (hourglass): glyph.svg, dot.svg, AppIcon.svg (1024 master), AppIcon-small.svg (16/32 px)."""
import sys, os, json
import numpy as np
from shapely.geometry import Point, Polygon
from shapely.ops import unary_union
from geo import path_d
from hg import build, chunk_ribbon

OUT = sys.argv[1] if len(sys.argv) > 1 else "out2/"
os.makedirs(OUT, exist_ok=True)
CONNECTED = "--connected" in sys.argv
sys.argv = [a for a in sys.argv if a != "--connected"]
BASE = dict(shape="spline", junction="base", jpos=-1.9, X=6.4, Y=6.6, a=1.4, e=3.2, ws=2.0, chamfer=3.0,
            width=1.6, tail=0.95, taper=3.5, gap_cross=0.55, close=0.9, taper_from_knock=True)
if len(sys.argv) > 2:
    BASE.update(json.loads(sys.argv[2]))
from shapely import affinity


def rot180(q, frame):
    """Turn the built mark 180 degrees about the frame centre (the dot ends up on the top bar)."""
    q = dict(q)
    q["stroke"] = affinity.rotate(q["stroke"], 180, origin=(frame / 2, frame / 2))
    q["pts"] = frame - q["pts"]
    q["head"] = (frame - q["head"][0], frame - q["head"][1])
    return q


def knockout(q, r, gap, connected):
    """What to cut from the stroke around the dot: the whole gap ring, or (connected) only the dot itself plus the
    gap on the tail side, so the stroke's head runs straight into the dot."""
    hc = q["head"]
    if not connected:
        return Point(hc).buffer(r + gap, 128)
    pts = q["pts"]
    ring = Point(hc).buffer(r + gap, 128)
    # the tail leaves the dot on one side of the line through its centre (perpendicular to the bar); cut only there
    i_exit = int(np.argmax(np.linalg.norm(pts - np.array(hc), axis=1) > r + gap))
    v = pts[i_exit] - np.array(hc); v /= np.linalg.norm(v)
    nrm = np.array([-v[1], v[0]])
    big = 4 * (r + gap)
    c = np.array(hc)
    tail = Polygon([c + nrm * big, c + nrm * big + v * big, c - nrm * big + v * big, c - nrm * big])
    return unary_union([Point(hc).buffer(r, 128), ring.intersection(tail)])


f3 = lambda v: f"{v:.3f}".rstrip("0").rstrip(".")

# ---------- menu bar glyph + dot (18x18 template images) ----------
G = dict(BASE, dot_r=2.7, gap=0.75, extent=16.9)
q = rot180(build(G, 18.0), 18.0)
hc = q["head"]
glyph = q["stroke"].difference(knockout(q, G["dot_r"], G["gap"], CONNECTED))
glyph = glyph.buffer(-0.04, 16).buffer(0.04, 16).simplify(0.004)   # drop hairline slivers left by the boolean ops
GAPNOTE = (f"The stroke's head runs straight into the dot (it stops at the dot's edge, r={f3(G['dot_r'])}); only the tail side keeps a "
           f"{f3(G['gap'])}px gap (r={f3(G['dot_r'] + G['gap'])}).") if CONNECTED else (
           f"A {f3(G['gap'])}px ring around the dot is knocked out (r={f3(G['dot_r'] + G['gap'])}) so it stays legible.")
open(OUT + "glyph.svg", "w").write(f'''<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 18 18">
  <!-- Process Automator menu bar glyph (template image): one colour, transparent background.
       One continuous stroke drawn as an hourglass: it leaves the dot to the right along the top (slightly tapered), runs down
       the "/" diagonal, rounds the base, crosses back over the waist on the "\\" diagonal (with a {f3(G["gap_cross"] * q["k"])}px
       over-under gap), and runs right along the top into the status dot, which is the stroke's head
       (dot.svg: r={f3(G["dot_r"])} at ({f3(hc[0])}, {f3(hc[1])})). {GAPNOTE}
       Outlined to a single path so AppKit's SVG renderer needs no masks or strokes. -->
  <path fill="#000" fill-rule="evenodd" d="{path_d(glyph, 2)}"/>
</svg>
''')
open(OUT + "dot.svg", "w").write(f'''<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 18 18">
  <!-- Process Automator status dot: the head of the glyph's hourglass stroke, on its top edge. Drawn over glyph.svg
       ({"head side joined to the dot, gap on the tail side" if CONNECTED else "which has the matching gap ring around the same centre"}).
       The app recolours the fill (systemGreen = all routines OK, systemRed = error, systemGray = unknown/idle). -->
  <circle cx="{f3(hc[0])}" cy="{f3(hc[1])}" r="{f3(G["dot_r"])}" fill="#000"/>
</svg>
''')

# ---------- app icon: the mark itself on transparency ----------
STOPS = [(0.0, (0x9B, 0x6B, 0xFF)), (0.45, (0x4C, 0x7D, 0xFF)), (1.0, (0x1F, 0xC8, 0xE3))]  # violet -> blue -> cyan


def colour(v):
    for (a, ca), (b, cb) in zip(STOPS, STOPS[1:]):
        if v <= b:
            t = (v - a) / (b - a)
            return "#%02X%02X%02X" % tuple(round(ca[i] + (cb[i] - ca[i]) * t) for i in range(3))
    return "#%02X%02X%02X" % STOPS[-1][1]


def icon(p, note, extent, dot_r, gap, shadow=True):
    p = dict(BASE, **p, dot_r=dot_r, extent=extent, gap=gap)
    q = rot180(build(p, 1024.0), 1024.0)
    pts, w, hc, k = q["pts"], q["w"], q["head"], q["k"]
    knock = knockout(q, dot_r, gap, CONNECTED)
    final = q["stroke"].difference(knock)
    final = final.buffer(-0.04 * k, 16).buffer(0.04 * k, 16)
    oa, ob = q["over"]
    widen = 2 * q["close"] + 0.25 * k
    N = 360
    n = len(pts)
    # colour parameter: advances along the straight runs and holds through the corners (where the slices fan out
    # around a tight turn, a changing colour would show as a crease)
    tg = np.gradient(pts, axis=0); tg /= np.linalg.norm(tg, axis=1)[:, None]
    turn = np.abs(np.gradient(np.unwrap(np.arctan2(tg[:, 1], tg[:, 0]))))   # radians per sample
    ds_ = np.linalg.norm(np.gradient(pts, axis=0), axis=1)
    curv = turn / np.maximum(ds_, 1e-9)                                       # 1 / radius
    wgt = 1 / (1 + (curv * (w / 2 + q["close"]) * 2.5) ** 2)
    wgt = np.convolve(np.r_[wgt[:30][::-1], wgt, wgt[-30:][::-1]], np.ones(61) / 61, mode="same")[30:-30]
    cpar = np.cumsum(wgt * ds_); cpar = (cpar - cpar[0]) / (cpar[-1] - cpar[0])
    # exact-width slices carry the colour; the corner-rounding fill they leave uncovered is underpainted from wider
    # slices first, so no wide slice ever paints over a neighbour's exact area (no visible seams)
    exact, wide = [], []
    for i in range(N):
        a, e = int(i * n / N), min(n, int((i + 1) * n / N) + 10)
        in_over = e > oa and a < ob
        a = max(a, q["i0"])
        if e - a < 2:
            continue
        ci = cpar[min(n - 1, (a + e) // 2)]
        exact.append((ci, chunk_ribbon(pts, w + 0.02 * k, (a, e), step=8).intersection(final)))
        wide.append((ci, None if in_over else chunk_ribbon(pts, w + widen, (a, e), step=8)))
    fill = final.difference(unary_union([g for _, g in exact]).buffer(0.01 * k))
    paths = []
    for i, g in wide:
        if g is not None:
            g = g.intersection(fill.buffer(0.08 * k)).intersection(final)
            if not g.is_empty and g.area > 0.5:
                paths.append(f'<path fill="{colour(i)}" d="{path_d(g, 1)}"/>')
    for i, g in exact:
        if not g.is_empty:
            paths.append(f'<path fill="{colour(i)}" d="{path_d(g, 1)}"/>')
    sh = ('<filter id="shadow" x="-20%" y="-20%" width="140%" height="140%"><feDropShadow dx="0" dy="10" stdDeviation="14" '
          'flood-color="#000" flood-opacity="0.28"/></filter>') if shadow else ""
    return f'''<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
<!-- Process Automator app icon{note}{" (variant: the stroke's head joins the dot)" if CONNECTED else ""}: the hourglass mark itself on a transparent background.
     Same geometry as the menu bar glyph (glyph.svg). The stroke is coloured along its length (violet at the tail, blue,
     cyan at the head) in {N} overlapping slices drawn in stroke order, so the second pass over the waist sits on top; the
     status dot (systemGreen) is the stroke's head. -->
<defs>
  <radialGradient id="dot" cx="0.38" cy="0.32" r="0.75">
    <stop offset="0" stop-color="#6EEB8E"/><stop offset="0.55" stop-color="#34C759"/><stop offset="1" stop-color="#239E48"/>
  </radialGradient>
  {sh}
</defs>
<g{' filter="url(#shadow)"' if shadow else ''}>
  {"".join(paths)}
  <circle cx="{hc[0]:.2f}" cy="{hc[1]:.2f}" r="{dot_r:.2f}" fill="url(#dot)"/>
</g>
</svg>
'''


open(OUT + "AppIcon.svg", "w").write(icon({}, ", 1024 master", 840.0, 2.55 / 16.9 * 840, 0.7 / 16.9 * 840))
open(OUT + "AppIcon-small.svg", "w").write(
    icon(dict(width=2.15, tail=1.3, taper=3.0, gap_cross=0.7, close=0.75), " - small sizes (16, 32 px): heavier stroke", 900.0,
         2.9 / 16.9 * 900, 0.85 / 16.9 * 900, shadow=False))
print("glyph head", [round(v, 3) for v in hc], "stroke", round(G["width"] * q["k"], 2))
