#!/usr/bin/env python3
"""Generate docs/static/img/compat-matrix.svg — a heatmap of the daemon
versions covered by the test matrices.

Reads tests/{bird,frr}-matrix/versions.txt (which releases are exercised)
and tests/{bird,frr}-matrix/xfail.txt (which have known failures) so the
chart can't drift from what CI actually runs. Cairn is a single-version
private-beta backend listed explicitly.

Cell colours:
  green  — every generated config passes on this release
  amber  — release is tested but has documented exceptions (xfail.txt)

Usage: tests/gen-compat-heatmap.py
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "docs" / "static" / "img" / "compat-matrix.svg"

PASS = "#27ae60"    # all configs pass
PARTIAL = "#e67e22"  # tested, documented exceptions (xfail)
TEXT = "#2c3e50"
MUTED = "#7f8c8d"
GRID = "#ecf0f1"


def read_versions(path: Path) -> list[str]:
    """Data lines of a versions.txt: first whitespace field per non-comment line."""
    out = []
    for line in path.read_text().splitlines():
        line = line.split("#", 1)[0].strip()
        if not line:
            continue
        out.append(line.split()[0])
    return out


def read_xfail(path: Path) -> set[str]:
    """Version keys (normalized) that have at least one xfail entry."""
    keys = set()
    if not path.exists():
        return keys
    for line in path.read_text().splitlines():
        line = line.split("#", 1)[0].strip()
        if not line:
            continue
        keys.add(norm(line.split()[0]))
    return keys


def norm(tag: str) -> str:
    """'v2.0.7'/'frr-9.1.3'/'2.0.7' -> '2.0.7' canonical key."""
    t = tag.strip()
    for p in ("frr-", "v"):
        if t.startswith(p):
            t = t[len(p):]
            break
    return t


def label(tag: str) -> str:
    """Short display label: 'v2.0.7'->'2.0.7', 'frr-10.4.5'->'10.4.5'."""
    return norm(tag)


def esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def render(rows: list[tuple[str, list[tuple[str, str]]]]) -> str:
    """rows: [(backend_name, [(tag, status), ...]), ...] status in pass|partial."""
    cell_w, cell_h, gap = 40, 26, 5
    label_w, pad_l, pad_r, pad_t = 74, 16, 16, 46
    row_gap = 18
    legend_h = 40

    max_cells = max(len(cells) for _, cells in rows)
    width = pad_l + label_w + max_cells * (cell_w + gap) - gap + pad_r
    height = pad_t + len(rows) * (cell_h + row_gap) - row_gap + legend_h + 8

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
        f'viewBox="0 0 {width} {height}" font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif">',
        f'<rect width="{width}" height="{height}" fill="#ffffff"/>',
        f'<text x="{pad_l}" y="26" font-size="15" font-weight="700" fill="{TEXT}">'
        f'Pathvector NG — routing-daemon compatibility</text>',
        f'<text x="{pad_l}" y="42" font-size="10.5" fill="{MUTED}">'
        f'Each cell is a daemon release exercised by the automated test matrix (generate → daemon check → live daemon → control commands).</text>',
    ]

    y = pad_t
    for name, cells in rows:
        # backend label
        parts.append(
            f'<text x="{pad_l}" y="{y + cell_h / 2 + 4:.0f}" font-size="12" font-weight="600" fill="{TEXT}">{esc(name)}</text>'
        )
        x = pad_l + label_w
        for tag, status in cells:
            fill = PASS if status == "pass" else PARTIAL
            parts.append(
                f'<rect x="{x}" y="{y}" width="{cell_w}" height="{cell_h}" rx="4" fill="{fill}"/>'
            )
            parts.append(
                f'<text x="{x + cell_w / 2}" y="{y + cell_h / 2 + 3.5:.0f}" font-size="9.5" '
                f'text-anchor="middle" fill="#ffffff" font-weight="600">{esc(label(tag))}</text>'
            )
            x += cell_w + gap
        y += cell_h + row_gap

    # legend
    ly = height - legend_h + 6
    parts.append(f'<rect x="{pad_l}" y="{ly}" width="11" height="11" rx="2" fill="{PASS}"/>')
    parts.append(f'<text x="{pad_l + 16}" y="{ly + 10}" font-size="10" fill="{MUTED}">all configs pass</text>')
    lx = pad_l + 150
    parts.append(f'<rect x="{lx}" y="{ly}" width="11" height="11" rx="2" fill="{PARTIAL}"/>')
    parts.append(f'<text x="{lx + 16}" y="{ly + 10}" font-size="10" fill="{MUTED}">tested — documented exceptions</text>')

    parts.append("</svg>")
    return "\n".join(parts) + "\n"


def main() -> int:
    bird_v = read_versions(ROOT / "tests/bird-matrix/versions.txt")
    frr_v = read_versions(ROOT / "tests/frr-matrix/versions.txt")
    bird_x = read_xfail(ROOT / "tests/bird-matrix/xfail.txt")
    frr_x = read_xfail(ROOT / "tests/frr-matrix/xfail.txt")

    def cells(versions, xfails):
        return [(t, "partial" if norm(t) in xfails else "pass") for t in versions]

    rows = [
        ("BIRD  (backend: bird)", cells(bird_v, bird_x)),
        ("FRR   (backend: frr)", cells(frr_v, frr_x)),
        ("Cairn (backend: cairn)", [("0.1.1", "pass")]),
    ]

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(render(rows))
    print(f"wrote {OUT.relative_to(ROOT)} "
          f"({len(bird_v)} BIRD, {len(frr_v)} FRR, 1 Cairn cells)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
