"""Pick long-tail titles a viewer might type exactly: export titles outside
the Some library whose folded name is unique in the whole export, has two to
five words, is Latin script, and shares no word sequence with a library
title's beginning. Deterministic (seeded) sample.

usage: python3 -I pick_tail.py <exportsdir> <n> >> intents.txt
"""
import gzip, json, random, re, sys, unicodedata

exp, n = sys.argv[1], int(sys.argv[2])


def fold(s):
    s = unicodedata.normalize('NFKD', s.lower())
    s = ''.join(c for c in s if not unicodedata.combining(c))
    s = re.sub(r"['’`.·]", '', s.replace('&', ' and '))
    return ' '.join(re.sub(r'[\W_]+', ' ', s).split())


some = {'m': set(json.load(open(exp + '/some_movie.json'))), 'tv': set(json.load(open(exp + '/some_tv.json')))}
names, count = {}, {}
for kind, f, field in (('m', 'movie_ids_11_07.json.gz', 'original_title'), ('tv', 'tv_series_ids_11_07.json.gz', 'original_name')):
    for line in gzip.open(exp + '/' + f, 'rt'):
        r = json.loads(line)
        if r.get('adult'):
            continue
        nm = r.get(field) or ''
        fn = fold(nm)
        count[fn] = count.get(fn, 0) + 1
        if r['id'] in some[kind]:
            names.setdefault('lib', set()).add(fn)
            continue
        if 0.6 <= (r.get('popularity') or 0) < 5 and not r.get('video'):
            names.setdefault('cand', []).append((kind, r['id'], nm, fn, r.get('popularity')))
lib = names['lib']
lib_prefixes = set()
for fn in lib:
    w = fn.split()
    for i in range(1, len(w) + 1):
        lib_prefixes.add(' '.join(w[:i]))
cands = [c for c in names['cand'] if count[c[3]] == 1 and 2 <= len(c[3].split()) <= 5 and re.fullmatch(r'[a-z0-9 ]+', c[3])
         and c[3] not in lib_prefixes and c[3].split()[0] not in ('the', 'a', 'an')]
random.Random(20261108).shuffle(cands)
for kind, i, nm, fn, pop in sorted(cands[:n], key=lambda c: c[3]):
    print('tail | %s | | %s#%d' % (nm.replace('|', ' '), kind, i))
