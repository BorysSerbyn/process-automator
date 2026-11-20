"""Hourglass mark (round 2): one continuous figure-8 stroke drawn as an hourglass; the status dot is the stroke's head.

Centreline (unit space, waist at the origin, y down): bulb sides leave the corners vertically and sweep through the
waist as an S; filleted corners; flat top and base. The stroke starts (thin, tapering in) at the junction J in the
bottom-left, climbs through the waist, runs round the top bulb, crosses back over the waist, round the bottom bulb and
along the base back to J, where it ends in the dot.
"""
import numpy as np
from shapely.geometry import Polygon, Point
from shapely.ops import unary_union
from shapely import affinity

DEF = dict(X=4.6, Y=6.6, r=1.7, h=3.4, m=2.2, s=1.25, width=1.6, tail=0.3, taper=5.5,
           junction="corner", jpos=0.0, gap_cross=0.55, dot_r=2.6, gap=0.8, extent=16.9, head_swell=0.0, grow=None, grow_e=1.0, shape="bulb", bow=0.0, contrast=None, pen=45.0, contrast_e=1.5, a=1.2, e=1.5, close=0.0)


def bez(p0, p1, p2, p3, n=200):
    t = np.linspace(0, 1, n)[:, None]
    return (1 - t) ** 3 * p0 + 3 * (1 - t) ** 2 * t * p1 + 3 * (1 - t) * t ** 2 * p2 + t ** 3 * p3


def arc_corner(p_from, corner, p_to, n=60):
    """Rounded corner from p_from to p_to with control towards the corner point (circle-ish quarter)."""
    k = 0.5523
    p_from, corner, p_to = map(np.asarray, (p_from, corner, p_to))
    return bez(p_from, p_from + k * (corner - p_from), p_to + k * (corner - p_to), p_to, n)


def centreline_glass(p):
    """Classic hourglass: two diagonals crossing at the waist (optionally bowed), flat top and base, round corners.
    Corner fillets are cubic with trim length r along each side; bow pushes each half-diagonal sideways."""
    X, Y, r, bow = p["X"], p["Y"], p["r"], p["bow"]
    A = lambda x, y: np.array([x, y], float)
    W = A(0, 0)
    BL, TR, TL, BR = A(-X, Y), A(X, -Y), A(-X, -Y), A(X, Y)
    def toward(a, b, dist):
        v = b - a; return a + v / np.linalg.norm(v) * dist
    def half_diag(a, b):  # a corner-trimmed point -> waist (or reverse), bowed outward from the axis
        mid = (a + b) / 2
        nrm = np.array([-(b - a)[1], (b - a)[0]]); nrm /= np.linalg.norm(nrm)
        if np.dot(nrm, A(np.sign(mid[0]) or 1, 0)) < 0: nrm = -nrm
        c = mid + nrm * bow
        tq = np.linspace(0, 1, 120)[:, None]
        return (1 - tq) ** 2 * a + 2 * (1 - tq) * tq * c + tq ** 2 * b
    def base_curve(a, b, sag):
        if not sag:
            return np.linspace(a, b, 80)
        c = (a + b) / 2 + A(0, 2 * sag)   # quadratic control: the base sags by `sag` at the centre
        tq = np.linspace(0, 1, 160)[:, None]
        return (1 - tq) ** 2 * a + 2 * (1 - tq) * tq * c + tq ** 2 * b
    def fillet(prev_pt, corner, next_pt):
        a, b = toward(corner, prev_pt, r), toward(corner, next_pt, r)
        return a, b, arc_corner(a, corner, b)
    # corner trims
    bl_a, bl_b, bl_f = fillet(BR, BL, W)      # base -> BL -> up the "/" side
    tr_a, tr_b, tr_f = fillet(W, TR, TL)
    tl_a, tl_b, tl_f = fillet(TR, TL, W)
    br_a, br_b, br_f = fillet(W, BR, BL)
    segs = [
        bl_f, half_diag(bl_b, W), half_diag(W, tr_a)[::-1][::-1], tr_f,
        np.linspace(tr_b, tl_a, 80), tl_f, half_diag(tl_b, W), half_diag(W, br_a), br_f,
        base_curve(br_b, bl_a, p.get("sag", 0.0)),
    ]
    # start the loop on the base (just after the BL fillet would start): rotate so the base is last
    segs = segs[1:] + [segs[0]]  # now starts at bl_b (going up), ends at bl_b
    segs = [np.linspace(bl_a, bl_a, 1)] if False else segs
    return segs


def centreline_spline(p):
    """Closed uniform cubic B-spline over an hourglass control polygon: continuous curvature everywhere, so corners
    flow into the sides. a = how far either side of the waist the diagonal is held straight; e = how far the top and
    base hold flat before turning."""
    X, Y, a, e = p["X"], p["Y"], p["a"], p["e"]; s = Y / X * p.get("ws", 1.0)
    A = lambda x, y: np.array([x, y], float)
    ctrl = [A(-X, Y), A(-a, a * s), A(a, -a * s), A(X, -Y), A(X - e, -Y), A(-X + e, -Y), A(-X, -Y),
            A(-a, -a * s), A(a, a * s), A(X, Y), A(X - e, Y), A(-X + e, Y)]
    c = p.get("chamfer", 0.0)
    if c:  # split each outer corner control point in two, along its edges: rounder, curvier corners
        corners = {0, 3, 6, 9}
        new = []
        for i, P in enumerate(ctrl):
            if i in corners:
                prv, nxt = ctrl[i - 1], ctrl[(i + 1) % len(ctrl)]
                u1 = (prv - P) / np.linalg.norm(prv - P); u2 = (nxt - P) / np.linalg.norm(nxt - P)
                new += [P + c * u1, P + c * u2]
            else:
                new.append(P)
        ctrl = new
    n = len(ctrl)
    out = []
    for i in range(n):
        P0, P1, P2, P3 = ctrl[i - 1], ctrl[i], ctrl[(i + 1) % n], ctrl[(i + 2) % n]
        tq = np.linspace(0, 1, 120, endpoint=False)[:, None]
        b0 = (1 - tq) ** 3 / 6; b1 = (3 * tq ** 3 - 6 * tq ** 2 + 4) / 6
        b2 = (-3 * tq ** 3 + 3 * tq ** 2 + 3 * tq + 1) / 6; b3 = tq ** 3 / 6
        out.append(b0 * P0 + b1 * P1 + b2 * P2 + b3 * P3)
    pts = np.concatenate(out)
    # start the loop just after the base (segment i=0 starts between BLm.. and BL): rotate so it starts at bottom-left
    return [np.concatenate([pts, pts[:1]])]


def centreline_lobe(p):
    """Each half-bulb is one cubic from the waist to the end of a flat bar: it leaves the waist along the diagonal
    (angle `wa` from horizontal, handle `m`) and arrives at the bar horizontally (handle `q`), so side and corner are
    one continuous sweep. Bars are flat between x = -b and b at y = -Y (top) and Y (base)."""
    Y, b, q, m = p["Y"], p["b"], p["q"], p["m"]
    ang = np.radians(p["wa"])
    W = np.array([0.0, 0.0])
    up = np.array([np.cos(ang), -np.sin(ang)])    # "/" heading up-right
    dn = np.array([np.cos(ang), np.sin(ang)])     # "\\" heading down-right
    A = lambda x, y: np.array([x, y], float)
    segs = [
        bez(W, W + m * up, A(b + q, -Y), A(b, -Y)),           # waist -> top-right -> top bar
        np.linspace(A(b, -Y), A(-b, -Y), 80),
        bez(A(-b, -Y), A(-b - q, -Y), W - m * dn, W),          # top-left -> waist
        bez(W, W + m * dn, A(b + q, Y), A(b, Y)),             # waist -> bottom-right -> base
        np.linspace(A(b, Y), A(-b, Y), 80),
        bez(A(-b, Y), A(-b - q, Y), W - m * up, W),           # bottom-left -> waist
    ]
    return segs


def centreline(p):
    if p.get("shape") in ("glass", "spline", "lobe"):
        segs = {"glass": centreline_glass, "spline": centreline_spline, "lobe": centreline_lobe}[p["shape"]](p)
        segs = centreline_glass(p)
        pts = np.concatenate([sg[:-1] for sg in segs] + [segs[-1][-1:]])
        d = np.r_[0, np.cumsum(np.linalg.norm(np.diff(pts, axis=0), axis=1))]
        L = d[-1]
        u = np.linspace(0, L, 3000)
        pts = np.stack([np.interp(u, d, pts[:, 0]), np.interp(u, d, pts[:, 1])], 1)
        return pts, u, L
    X, Y, r, h, m, s = p["X"], p["Y"], p["r"], p["h"], p["m"], p["s"]
    W = np.array([0.0, 0.0])
    d1 = np.array([1.0, -s]); d1 /= np.linalg.norm(d1)   # "/" through the waist, heading up-right
    d2 = np.array([1.0, s]); d2 /= np.linalg.norm(d2)    # "\" through the waist, heading down-right
    A = lambda x, y: np.array([x, y], float)
    segs = [
        bez(A(-X, Y - r), A(-X, Y - r - h), W - m * d1, W),            # bottom-left side up to the waist
        bez(W, W + m * d1, A(X, -Y + r + h), A(X, -Y + r)),            # waist up to the top-right
        arc_corner(A(X, -Y + r), A(X, -Y), A(X - r, -Y)),
        np.linspace(A(X - r, -Y), A(-X + r, -Y), 80),                  # top
        arc_corner(A(-X + r, -Y), A(-X, -Y), A(-X, -Y + r)),
        bez(A(-X, -Y + r), A(-X, -Y + r + h), W - m * d2, W),          # top-left side down to the waist
        bez(W, W + m * d2, A(X, Y - r - h), A(X, Y - r)),              # waist down to the bottom-right
        arc_corner(A(X, Y - r), A(X, Y), A(X - r, Y)),
        np.linspace(A(X - r, Y), A(-X + r, Y), 80),                    # base, heading left
        arc_corner(A(-X + r, Y), A(-X, Y), A(-X, Y - r)),
    ]
    pts = np.concatenate([sg[:-1] for sg in segs] + [segs[-1][-1:]])
    # resample by arc length
    d = np.r_[0, np.cumsum(np.linalg.norm(np.diff(pts, axis=0), axis=1))]
    L = d[-1]
    u = np.linspace(0, L, 3000)
    pts = np.stack([np.interp(u, d, pts[:, 0]), np.interp(u, d, pts[:, 1])], 1)
    # the waist passes: first and second crossing of the origin
    near = np.where(np.linalg.norm(pts, axis=1) < 0.05)[0]
    return pts, u, L


def rotate_start(pts, u, L, j_idx):
    """Re-start the closed loop at index j_idx."""
    pts = np.concatenate([pts[j_idx:-1], pts[:j_idx + 1]])
    d = np.r_[0, np.cumsum(np.linalg.norm(np.diff(pts, axis=0), axis=1))]
    return pts, d


def chunk_ribbon(pts, w, idx_range, step=12):
    a, b = idx_range
    polys = []
    for i in range(a, b - 1, step):
        e = min(b, i + step + 2)
        seg, ww = pts[i:e], w[i:e]
        if len(seg) < 2:
            continue
        t = np.gradient(seg, axis=0) if len(seg) > 2 else np.repeat(seg[1:] - seg[:1], len(seg), 0)
        t = t / np.linalg.norm(t, axis=1)[:, None]
        n = np.stack([-t[:, 1], t[:, 0]], 1)
        left, right = seg + n * ww[:, None] / 2, seg - n * ww[:, None] / 2
        polys.append(Polygon(np.concatenate([left, right[::-1]])).buffer(0))
    return unary_union(polys)


def build(p=None, frame=18.0):
    p = dict(DEF, **(p or {}))
    pts, u, L = centreline(p)
    # junction: bottom-left corner (index of the corner arc midpoint) or along the base
    if p["junction"] == "corner":
        corner = np.array([-p["X"], p["Y"]]) + np.array([1, -1]) * p["r"] * (1 - 0.7071)
        j = int(np.argmin(np.linalg.norm(pts - corner, axis=1)))
    else:  # on the base at x = jpos
        ymax = pts[:, 1].max()
        cand = np.where(pts[:, 1] > ymax - 0.12)[0]
        j = int(cand[np.argmin(np.abs(pts[cand, 0] - p["jpos"]))])
    pts, d = rotate_start(pts, u, L, j)
    L = d[-1]
    head = pts[-1]
    # waist crossings: first and second time the path passes the origin
    order = np.argsort(np.linalg.norm(pts, axis=1))
    i1 = order[0]
    i2 = next(i for i in order if abs(d[i] - d[i1]) > 3)
    i1, i2 = sorted([i1, i2])
    span = int(2.6 / (L / len(pts)))
    under = (max(0, i1 - span), i1 + span)                    # first pass goes under
    overr = (i2 - span, i2 + span)
    k, d0 = 1.0, 0.0
    for _ in range(3):
        # width: tail point -> full width over `taper`, measured from where the stroke leaves the dot's knockout
        ds = d0 + p.get("skip", 0.0)                       # the stroke starts here (after the knockout + skip)
        i0 = int(np.searchsorted(d, ds - (1.5 if not p.get("skip") else 0))) if ds > 0 else 0  # start inside the knockout
        tp = np.clip((d - ds) / p["taper"], 0, 1)
        w = p["tail"] + (p["width"] - p["tail"]) * (1 - (1 - tp) ** 2)
        full = chunk_ribbon(pts, w, (i0, len(pts)))
        # Each half of the loop (split midway between the two waist passes) is built and corner-rounded on its own;
        # the first pass over the waist (in half A) then gets the over-under gap cut around the second pass (half B).
        mid = (i1 + i2) // 2
        halves = [chunk_ribbon(pts, w, (i0, mid + 12)), chunk_ribbon(pts, w, (mid - 12, len(pts)))]
        if p["close"]:
            halves = [h.buffer(p["close"], 32).buffer(-p["close"], 32) for h in halves]
        A, B = halves
        over = B.intersection(chunk_ribbon(pts, w * 3, overr))
        if p["gap_cross"] > 0:
            A = A.difference(over.buffer(p["gap_cross"], 24))
        full = stroke = unary_union([A, B])
        for _ in range(8):
            dot = Point(head).buffer(p["dot_r"] / k, 64)
            b = unary_union([stroke, dot]).bounds
            k = p["extent"] / max(b[2] - b[0], b[3] - b[1])
        if not p.get("taper_from_knock"):
            break
        rk = (p["dot_r"] + p["gap"]) / k
        d0 = d[np.argmax(np.linalg.norm(pts - head, axis=1) > rk)]
    cx, cy = (b[0] + b[2]) / 2, (b[1] + b[3]) / 2
    T = lambda g: affinity.translate(affinity.scale(g, k, k, origin=(0, 0)), frame / 2 - k * cx, frame / 2 - k * cy)
    hc = (head[0] * k + frame / 2 - k * cx, head[1] * k + frame / 2 - k * cy)
    stroke_t = T(stroke)
    return dict(stroke=stroke_t, head=hc, k=k, p=p, pts=pts * k + np.array([frame / 2 - k * cx, frame / 2 - k * cy]),
                w=w * k, d=d * k, under=under, over=overr, T=T, close=p['close'] * k, i0=i0)
