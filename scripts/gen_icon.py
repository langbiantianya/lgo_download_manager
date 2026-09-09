#!/usr/bin/env python3
"""Generate the ldm installer/tray icon as a multi-resolution .ico file.

Run from repo root:

    python scripts/gen_icon.py

Writes assets/ldm.ico with PNG-encoded frames at 16/24/32/48/64/128/256.
ICO containers holding PNG payloads (rather than BMP) are supported by
every Windows version since Vista, including the ones Inno Setup ships
to.

The graphic is a downward arrow (download metaphor) on a rounded indigo
square — no external asset is required, so the icon is regenerable from
scratch by anyone with Pillow installed.
"""
from __future__ import annotations

import io
import os
import struct
import sys
from PIL import Image, ImageDraw

# Brand palette — kept in sync with the tray icon's fyne template fallback.
BG = (79, 70, 229, 255)        # indigo-600
FG = (255, 255, 255, 255)      # white
ACCENT = (129, 140, 248, 255)  # indigo-400

SIZES = (16, 24, 32, 48, 64, 128, 256)


def render(size: int) -> Image.Image:
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # Rounded square background. Corner radius scales with the size.
    radius = max(2, size // 5)
    d.rounded_rectangle((0, 0, size - 1, size - 1), radius=radius, fill=BG)

    # Inner highlight rim — a thin lighter outline near the edge.
    rim = max(1, size // 32)
    d.rounded_rectangle(
        (rim, rim, size - 1 - rim, size - 1 - rim),
        radius=max(1, radius - rim),
        outline=ACCENT,
        width=max(1, size // 64),
    )

    # Downward arrow: shaft + chevron tip.
    margin = size * 0.22
    shaft_w = max(2, size * 0.14)
    shaft_top = margin
    shaft_bottom = size * 0.62
    shaft_left = (size - shaft_w) / 2
    d.rectangle(
        (shaft_left, shaft_top, shaft_left + shaft_w, shaft_bottom),
        fill=FG,
    )

    tip_x = size / 2
    tip_y = size - margin
    wing = shaft_w * 1.9
    half_top = shaft_bottom
    d.polygon(
        [
            (tip_x - wing, half_top - wing * 0.55),
            (tip_x + wing, half_top - wing * 0.55),
            (tip_x, tip_y),
        ],
        fill=FG,
    )

    return img


def encode_png(img: Image.Image) -> bytes:
    buf = io.BytesIO()
    img.save(buf, format="PNG", optimize=True)
    return buf.getvalue()


def write_ico(path: str, frames: list[tuple[int, bytes]]) -> None:
    """Write an ICO container holding PNG payloads.

    ICO header: 6 bytes (reserved=0, type=1 for icon, count).
    Each directory entry: 16 bytes (w, h, ncolors, reserved, planes, bpp, size, offset).
    Followed by the raw image payloads (PNG bytes in our case).
    """
    count = len(frames)
    header = struct.pack("<HHH", 0, 1, count)
    dir_size = 16 * count
    offset = 6 + dir_size

    directory = b""
    payloads = b""
    for size, png in frames:
        # 0 means "256" in ICO's 1-byte size fields.
        w = 0 if size == 256 else size
        h = 0 if size == 256 else size
        directory += struct.pack(
            "<BBBBHHII",
            w, h,
            0,            # palette colors (0 for true-color / PNG)
            0,            # reserved
            1,            # color planes
            32,           # bits per pixel (informational for PNG payloads)
            len(png),
            offset,
        )
        payloads += png
        offset += len(png)

    with open(path, "wb") as f:
        f.write(header + directory + payloads)


def main() -> int:
    out_dir = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "assets")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, "ldm.ico")

    base = render(256)
    frames: list[tuple[int, bytes]] = []
    for s in SIZES:
        frame = base if s == 256 else base.resize((s, s), Image.LANCZOS)
        frames.append((s, encode_png(frame)))

    write_ico(out_path, frames)
    print(f"wrote {out_path} ({os.path.getsize(out_path)} bytes, sizes={SIZES})")
    return 0


if __name__ == "__main__":
    sys.exit(main())