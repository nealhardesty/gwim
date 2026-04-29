"""Stdlib-only mirror of `scripts/gen-icon/main.go`'s ICO step.

Use this when you've updated the hand-drawn
`internal/icon/assets/icon-{active,suspended}.png` source images but
don't have a Go toolchain on PATH to run `make icons`. It produces
byte-equivalent output to gen-icon: a single-entry PNG-in-ICO
container that wraps the source PNG payload verbatim, suitable for
the //go:embed directive in `internal/icon/icon_windows.go`.

Both paths are kept for redundancy — gen-icon is the canonical
build-time generator and is what `make icons` invokes; this Python
helper exists so artwork updates aren't blocked on a Go install.
"""

import os
import struct
import sys


def png_dimensions(data: bytes):
    """Return (width, height) from a PNG IHDR chunk."""
    if len(data) < 24 or data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("not a PNG file")
    width = struct.unpack(">I", data[16:20])[0]
    height = struct.unpack(">I", data[20:24])[0]
    return width, height


def encode_ico(png_paths, out_path):
    """Write an ICO file with one PNG-encoded entry per source PNG."""
    entries = []
    for path in png_paths:
        with open(path, "rb") as f:
            data = f.read()
        w, h = png_dimensions(data)
        entries.append((w, h, data))

    header_size = 6
    entry_size = 16
    data_offset = header_size + entry_size * len(entries)

    out = bytearray()
    out += struct.pack("<HHH", 0, 1, len(entries))

    offset = data_offset
    for w, h, data in entries:
        # ICO entry uses uint8 width/height; 0 means 256.
        wb = 0 if w >= 256 else w
        hb = 0 if h >= 256 else h
        out += struct.pack(
            "<BBBBHHII",
            wb,            # width
            hb,            # height
            0,             # color count
            0,             # reserved
            1,             # color planes
            32,            # bits per pixel
            len(data),     # bytes in payload
            offset,        # payload offset
        )
        offset += len(data)

    for _w, _h, data in entries:
        out += data

    with open(out_path, "wb") as f:
        f.write(bytes(out))


def main():
    base = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    asset_dir = os.path.join(base, "internal", "icon", "assets")
    pairs = [
        ("icon-active.png", "icon-active.ico"),
        ("icon-suspended.png", "icon-suspended.ico"),
    ]
    for src, dst in pairs:
        src_path = os.path.join(asset_dir, src)
        dst_path = os.path.join(asset_dir, dst)
        if not os.path.exists(src_path):
            print(f"skip: {src_path} not found", file=sys.stderr)
            continue
        encode_ico([src_path], dst_path)
        print(f"wrote {dst_path}")


if __name__ == "__main__":
    main()
