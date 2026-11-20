"""Shared geometry helpers: tapered ribbons along parametric centrelines, shapely -> SVG path."""
import math
import numpy as np
from shapely.geometry import Polygon, Point, MultiPolygon, LineString
from shapely.ops import unary_union


def ribbon(pts, widths):
    """Filled polygon of a ribbon along pts (N,2) with per-point full widths."""
    pts = np.asarray(pts, float)
    t = np.gradient(pts, axis=0)
    t /= np.linalg.norm(t, axis=1)[:, None]
    n = np.stack([-t[:, 1], t[:, 0]], axis=1)
    w = np.asarray(widths, float)[:, None] / 2
    left = pts + n * w
    right = pts - n * w
    ring = np.concatenate([left, right[::-1]])
    return Polygon(ring).buffer(0)


def spiral(c, r0, r1, a0, a1, n=900, ease=1.0):
    """Clockwise (screen coords) spiral from angle a0 to a1 (degrees from 12 o'clock), radius r0 -> r1."""
    s = np.linspace(0, 1, n)
    a = np.radians(a0 + (a1 - a0) * s)
    r = r0 + (r1 - r0) * s ** ease
    return np.stack([c[0] + r * np.sin(a), c[1] - r * np.cos(a)], axis=1), s


def circle(c, r, res=128):
    return Point(c).buffer(r, resolution=res)


def path_d(geom, prec=3):
    fmt = lambda v: f"{v:.{prec}f}".rstrip("0").rstrip(".")
    polys = list(geom.geoms) if isinstance(geom, MultiPolygon) else [geom]
    out = []
    for p in polys:
        for ring in [p.exterior, *p.interiors]:
            cs = list(ring.coords)[:-1]
            out.append("M" + " L".join(f"{fmt(x)} {fmt(y)}" for x, y in cs) + "Z")
    return "".join(out)


def svg(d_list, size=18, extra="", comment=""):
    body = "".join(f'<path fill="#000" fill-rule="evenodd" d="{d}"/>' for d in d_list)
    c = f"<!-- {comment} -->\n" if comment else ""
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{size}" height="{size}" viewBox="0 0 {size} {size}">\n'
            f"{c}{body}{extra}</svg>\n")
