#!/usr/bin/env python3
"""Rebuild the shipped faces from pinned Google Fonts sources.

Run with fonttools==4.68.0 in a venv. Existing files are always rebuilt.
Latin (including Extended/combining marks), Greek, Cyrillic and punctuation
cover the console's European locales. Noto Sans remains the glyph fallback;
CJK is separately managed. Unsupported glyphs are not invented by subsetting.
"""
import json
import pathlib
import tempfile
import urllib.request
from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

ROOT = pathlib.Path(__file__).resolve().parents[1] / 'internal/onair/fonts/data'
# families.Defaults, literal fonts in families/documents, and raster's fallback.
FAMILIES = ['inter', 'spacegrotesk', 'fraunces', 'playfairdisplay', 'bungee',
            'fredoka', 'specialelite', 'orbitron', 'vt323', 'ibmplexmono', 'notosans']
# Rename all modified faces, including RFNs Playfair Display, Orbitron and Plex.
# API/file IDs stay stable; embedded primary and PostScript names are distinct.
NAMES = {family: f'EtherAir{i+1:02d}' for i, family in enumerate(FAMILIES)}
UNICODES = (list(range(0x20, 0x250)) + list(range(0x300, 0x530)) +
            list(range(0x1e00, 0x2000)) + list(range(0x2000, 0x2070)) +
            [0x20ac, 0x2122, 0x2190, 0x2191, 0x2192, 0x2193, 0x2212, 0x25cf])


def main():
    rev = (ROOT / 'revision.txt').read_text().strip()
    for family in FAMILIES:
        location = 'apache' if family == 'specialelite' else 'ofl'
        base = f'https://api.github.com/repos/google/fonts/contents/{location}/{family}?ref={rev}'
        entries = json.load(urllib.request.urlopen(base))
        candidates = [e for e in entries if e['name'].endswith('.ttf') and 'Italic' not in e['name']]
        entry = next((e for e in candidates if 'Regular' in e['name']), candidates[0])
        with tempfile.NamedTemporaryFile() as tmp:
            tmp.write(urllib.request.urlopen(entry['download_url']).read())
            tmp.flush()
            font = TTFont(tmp.name, recalcTimestamp=False)
            if 'fvar' in font:
                axes = {a.axisTag: a.defaultValue for a in font['fvar'].axes}
                axes['wght'] = 900 if family == 'playfairdisplay' else axes.get('wght', 400)
                font = instantiateVariableFont(font, axes, inplace=True)
            opts = subset.Options()
            opts.layout_features = ['ccmp', 'locl', 'kern', 'liga', 'clig', 'mark', 'mkmk']
            opts.name_IDs = [0, 1, 2, 3, 4, 5, 6, 13, 14, 16, 17]  # retain copyright and licence metadata
            opts.hinting = False  # raster uses HintingNone
            sub = subset.Subsetter(options=opts)
            sub.populate(unicodes=UNICODES)
            sub.subset(font)
            for record in font['name'].names:
                if record.nameID in (1, 2, 3, 4, 6, 16, 17):
                    name = 'Regular' if record.nameID in (2, 17) else NAMES[family]
                    record.string = name.encode(record.getEncoding())
            font.save(ROOT / (family + '.ttf'))
        licence = next(e for e in entries if e['name'] in ('OFL.txt', 'LICENSE.txt'))
        text = urllib.request.urlopen(licence['download_url']).read().decode()
        (ROOT / (family + '-' + licence['name'])).write_text(
            '\n'.join(line.rstrip() for line in text.splitlines()) + '\n')
        print(family, flush=True)


if __name__ == '__main__':
    main()
