#!/usr/bin/env python3
"""Run in a venv with fonttools: fetch pinned Google OFL sources, instantiate, subset."""
import json, pathlib, urllib.request, tempfile
from fontTools.ttLib import TTFont
from fontTools import subset
from fontTools.varLib.instancer import instantiateVariableFont
ROOT = pathlib.Path(__file__).resolve().parents[1] / 'internal/onair/fonts/data'
FAMILIES = ['inter','spacegrotesk','fraunces','playfairdisplay','bungee','fredoka','specialelite','monoton','orbitron','vt323','archivoblack','ibmplexmono','notosans']
# Source revision is recorded beside the assets for reproducibility.
revfile = ROOT / 'revision.txt'
rev = revfile.read_text().strip() if revfile.exists() else json.load(urllib.request.urlopen('https://api.github.com/repos/google/fonts/commits/main'))['sha']
ROOT.mkdir(parents=True, exist_ok=True)
revfile.write_text(rev+'\n')
for family in FAMILIES:
    if (ROOT/(family+'.ttf')).exists(): continue
    location='apache' if family=='specialelite' else 'ofl'
    base=f'https://api.github.com/repos/google/fonts/contents/{location}/{family}?ref={rev}'
    entries=json.load(urllib.request.urlopen(base))
    candidates=[e for e in entries if e['name'].endswith('.ttf') and 'Italic' not in e['name']]
    entry=next((e for e in candidates if 'Regular' in e['name']), candidates[0])
    with tempfile.NamedTemporaryFile() as tmp:
        tmp.write(urllib.request.urlopen(entry['download_url']).read()); tmp.flush()
        font=TTFont(tmp.name)
        if 'fvar' in font:
            axes={a.axisTag:a.defaultValue for a in font['fvar'].axes}
            axes['wght']=900 if family=='playfairdisplay' else axes.get('wght',400)
            font=instantiateVariableFont(font, axes, inplace=True)
        opts=subset.Options(); opts.layout_features=['*']; opts.name_IDs=['*']
        sub=subset.Subsetter(options=opts)
        sub.populate(unicodes=list(range(0x250))+list(range(0x370,0x530))+list(range(0x1e00,0x2000))+list(range(0x2000,0x2070)))
        sub.subset(font); font.save(ROOT / (family+'.ttf'))
    license=next(e for e in entries if e['name'] in ('OFL.txt','LICENSE.txt'))
    (ROOT/(family+'-'+license['name'])).write_text('\n'.join(line.rstrip() for line in urllib.request.urlopen(license['download_url']).read().decode().splitlines())+'\n')
    print(family, flush=True)
