#!/usr/bin/env python3
"""Add synthetic HDR10+ SEI to a short local, video-only DV8.1 MKV.

The input must have unlaced SimpleBlocks with four-byte HEVC NAL lengths.
Use ffmpeg -map 0:v:0 -c copy to prepare it. No media is downloaded and no
Dolby Vision RPU or coded picture is generated or modified.
"""

from pathlib import Path
import struct
import sys


def hdr10plus_nal():
    # ST 2094-40: one window, 1000-nit target, no optional grids/curves.
    bits = "01" + format(1000, "027b") + "0" + "0" * 51 + "0" * 17
    bits += "0000" + "0" * 10 + "000"
    bits += "0" * (-len(bits) % 8)
    payload = bytes.fromhex("b5003c00010401")
    payload += int(bits, 2).to_bytes(len(bits) // 8, "big")
    rbsp = bytes([4, len(payload)]) + payload + b"\x80"
    escaped = bytearray()
    zeros = 0
    for value in rbsp:
        if zeros >= 2 and value <= 3:
            escaped.append(3)
            zeros = 0
        escaped.append(value)
        zeros = zeros + 1 if value == 0 else 0
    return b"\x4e\x01" + escaped  # prefix SEI NAL


def vint(data, offset, keep_marker=False):
    width = 1
    while width <= 8 and not data[offset] & (1 << (8 - width)):
        width += 1
    if width > 8 or offset + width > len(data):
        raise ValueError("invalid EBML integer")
    value = int.from_bytes(data[offset:offset + width], "big")
    return (value if keep_marker else value & ((1 << (7 * width)) - 1)), width


def size(value):
    for width in range(1, 9):
        if value < (1 << (7 * width)) - 1:
            return (value | (1 << (7 * width))).to_bytes(width, "big")
    raise ValueError("element too large")


def rewrite(data, insertion):
    output = bytearray()
    offset = 0
    while offset < len(data):
        element, width = vint(data, offset, True)
        length, size_width = vint(data, offset + width)
        start = offset + width + size_width
        # Unknown-size Segment/Cluster extends to the end of this short file.
        if length == (1 << (7 * size_width)) - 1:
            length = len(data) - start
        if start + length > len(data):
            raise ValueError("truncated element")
        body = data[start:start + length]
        offset = start + length
        if element in (0x114D9B74, 0x1C53BB6B, 0xBF):
            continue  # discard obsolete SeekHead, Cues and CRCs
        if element in (0x18538067, 0x1F43B675):
            body = rewrite(body, insertion)
        elif element == 0xA3:
            _, track_width = vint(body, 0)
            if body[track_width + 2] & 6:
                raise ValueError("laced blocks are unsupported")
            payload_start = track_width + 3
            body = body[:payload_start] + insertion + body[payload_start:]
        output.extend(element.to_bytes(width, "big") + size(len(body)) + body)
    return bytes(output)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit("usage: dv8-hdr10plus-fixture.py input-dv8.mkv output.mkv")
    nal = hdr10plus_nal()
    insertion = struct.pack(">I", len(nal)) + nal
    Path(sys.argv[2]).write_bytes(rewrite(Path(sys.argv[1]).read_bytes(), insertion))
