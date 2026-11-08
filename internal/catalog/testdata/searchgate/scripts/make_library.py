"""Write the gate's committed library: the Some walk's titles with TMDB's
title, original title, date, votes, rating, language and poster, and the
11-07 export's popularity (the value the two-tier sync stores).

usage: python3 -I make_library.py <library_raw.jsonl> <exportsdir> <out library.jsonl.gz>
"""
import gzip, json, sys

raw, exp, out = sys.argv[1:4]
pop = {}
for kind, name in (('m', 'movie_ids_11_07.json.gz'), ('tv', 'tv_series_ids_11_07.json.gz')):
    for line in gzip.open(exp + '/' + name, 'rt'):
        r = json.loads(line)
        pop[kind + ':' + str(r['id'])] = r.get('popularity') or 0
rows = {}
for line in open(raw):
    r = json.loads(line)
    k = ('m' if r['kind'] == 'movie' else 'tv') + ':' + str(r['id'])
    rec = dict(k=k, t=r['title'])
    if r['original'] and r['original'] != r['title']:
        rec['o'] = r['original']
    for src, dst in (('date', 'd'), ('votes', 'v'), ('vote_avg', 'r'), ('lang', 'l'), ('poster', 'pp')):
        if r.get(src):
            rec[dst] = r[src]
    rec['p'] = round(pop.get(k, r['pop']), 4)
    rows[k] = rec
with gzip.open(out, 'wt', compresslevel=9) as f:
    for k in sorted(rows, key=lambda k: (k.split(':')[0], int(k.split(':')[1]))):
        f.write(json.dumps(rows[k], ensure_ascii=False, separators=(',', ':')) + '\n')
missing = sum(1 for k in rows if k not in pop)
print('library', len(rows), 'titles;', missing, 'not in the 11-07 export (kept their /discover popularity)')
