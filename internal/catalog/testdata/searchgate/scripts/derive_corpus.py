"""Resolve corpus intents to TMDB ids and attach evidence from real votes and
popularity.

For each query the candidate pool is every library title and every title
TMDB's search returned for it. A candidate "matches" the query in a class:
3 the title (give or take a leading article), 2 the query begins the title,
1 the title holds the query's words (the last as a prefix). Then:
  - alt: other candidates in the expected top-1's class with votes within
    1.25x (and at least 50): equally good answers, either counts as top-1;
  - FLAGGED: a candidate in the same or a better class with more votes than
    the expected top-1 (not an alt). Every flag is reviewed by hand and the
    intent either fixed or kept with its reason (a typo, a show/movie pick).

usage: python3 -I derive_corpus.py <intents.txt> <library_raw.jsonl> <tmdb_search.jsonl> <out corpus.jsonl>
"""
import bisect, json, re, sys, unicodedata

intents_p, lib_p, rec_p, out_p = sys.argv[1:5]
_F = {}


def fold(s):
    r = _F.get(s)
    if r is None:
        t = unicodedata.normalize('NFKD', s.lower())
        t = ''.join(c for c in t if not unicodedata.combining(c))
        t = re.sub(r"['’`.·]", '', t.replace('&', ' and '))
        r = _F[s] = ' '.join(re.sub(r'[\W_]+', ' ', t).split())
    return r


FILLER = {'the', 'a', 'an', 'and', 'of', 'in'}
ART = ('the', 'a', 'an')
pool = {}


def put(k, title, original, year, votes, pop, lib):
    p = dict(key=k, title=title, original=original or title, year=year, votes=votes or 0, pop=pop or 0, lib=lib)
    p['names'] = list({fold(p['title']), fold(p['original'])} - {''})
    pool[k] = p


for line in open(lib_p):
    r = json.loads(line)
    put(('m' if r['kind'] == 'movie' else 'tv') + ':' + str(r['id']), r['title'], r['original'], (r['date'] or '')[:4],
        r['votes'], r['pop'], True)
rec = {}
for line in open(rec_p):
    r = json.loads(line)
    rec.setdefault((r['path'], r['query']), []).extend(r['results'])
    for x in r['results']:
        mt = x.get('media_type') or ('movie' if r['path'] == '/search/movie' else 'tv')
        if mt not in ('movie', 'tv'):
            continue
        k = ('m' if mt == 'movie' else 'tv') + ':' + str(x['id'])
        if k not in pool:
            put(k, x.get('title') or x.get('name') or '', x.get('original_title') or x.get('original_name') or '',
                (x.get('release_date') or x.get('first_air_date') or '')[:4], x.get('vote_count'), x.get('popularity'), False)

by_word, by_fold, by_join = {}, {}, {}
for p in pool.values():
    for n in p['names']:
        by_fold.setdefault(n, set()).add(p['key'])
        by_join.setdefault(n.replace(' ', ''), set()).add(p['key'])
        for w in set(n.split()):
            by_word.setdefault(w, set()).add(p['key'])
words_sorted = sorted(by_word)
joins_sorted = sorted(by_join)


def with_prefix(sorted_keys, index, pfx):
    i = bisect.bisect_left(sorted_keys, pfx)
    out = set()
    while i < len(sorted_keys) and sorted_keys[i].startswith(pfx):
        out |= index[sorted_keys[i]]
        i += 1
    return out


def joins(ws):
    s = {''.join(ws)}
    if len(ws) > 1 and ws[0] in ART:
        s.add(''.join(ws[1:]))
    return s


def match_class(q, p):
    qw = fold(q).split()
    if not qw:
        return 0
    content = [w for w in qw if w not in FILLER] or qw
    qj = joins(qw)
    best = 0
    for n in p['names']:
        nw = n.split()
        nj = joins(nw)
        if qj & nj:
            return 3
        if any(b.startswith(a) for a in qj for b in nj):
            best = max(best, 2)
            continue
        if all(any(x == w or (i == len(content) - 1 and x.startswith(w)) for x in nw) for i, w in enumerate(content)):
            best = max(best, 1)
    return best


def candidates(q):
    qw = [w for w in fold(q).split() if w not in FILLER] or fold(q).split()
    if not qw:
        return set()
    sets = [by_word.get(w, set()) for w in qw[:-1]] + [with_prefix(words_sorted, by_word, qw[-1])]
    cand = set.intersection(*sets)
    for j in joins(fold(q).split()):
        cand |= with_prefix(joins_sorted, by_join, j)
    return cand


def resolve(spec):
    d = re.match(r'^(m|tv)#(\d+)$', spec.strip())
    if d:
        k = d.group(1) + ':' + d.group(2)
        return pool.get(k) or dict(key=k, title='(export only)', original='', names=[], year='', votes=0, pop=0, lib=False)
    m = re.match(r'^(m|tv):(.*) \((\d{4})\)$', spec.strip())
    if not m:
        raise SystemExit('bad spec ' + spec)
    kind, title, year = m.groups()
    cands = [pool[k] for k in by_fold.get(fold(title), ()) if k.startswith(kind + ':')]
    exact = [p for p in cands if p['year'] == year] or [p for p in cands if p['year'] and abs(int(p['year']) - int(year)) <= 1]
    return max(exact, key=lambda p: (p['votes'], p['pop'])) if exact else None


def desc(p):
    return '%s %s (%s) %d votes%s' % (p['key'], p['title'], p['year'], p['votes'], '' if p['lib'] else ' [not in library]')


out = open(out_p, 'w')
flags, unresolved, n = [], [], 0
for line in open(intents_p):
    line = line.strip()
    if not line or line.startswith('#'):
        continue
    cls, q, types, targets = [p.strip() for p in line.split('|')]
    specs = [s for s in targets.split(';') if s.strip()]
    res = [resolve(s) for s in specs]
    if any(r is None for r in res):
        unresolved.append((cls, q, [s for s, r in zip(specs, res) if r is None]))
        continue
    kind = {'': None, 'm': 'm:', 'tv': 'tv:'}[types]
    path = {'': '/search/multi', 'm': '/search/movie', 'tv': '/search/tv'}[types]
    keys = {r['key'] for r in res}
    ck = {k for k in candidates(q) if k not in keys and (kind is None or k.startswith(kind))}
    tm = rec.get((path, q), [])
    for x in tm:
        mt = x.get('media_type') or ('movie' if path == '/search/movie' else 'tv')
        k = ('m' if mt == 'movie' else 'tv') + ':' + str(x['id'])
        if k in pool and k not in keys:
            ck.add(k)
    top = res[0]
    tc = match_class(q, top) if top['names'] else 3
    scored = [(match_class(q, pool[k]), pool[k]) for k in ck] + [(match_class(q, r), r) for r in res[1:] if r['names']]
    alts = sorted(p['key'] for c, p in scored if c == tc and tc > 0 and p['votes'] >= 50
                  and p['votes'] * 1.25 >= top['votes'] and p['votes'] <= top['votes'] * 1.25)
    stronger = [p for c, p in scored if c >= tc and c > 0 and p['votes'] > top['votes'] and p['key'] not in alts]
    rival = max((p for c, p in scored if c > 0), key=lambda p: p['votes'], default=None)
    why = 'top1 %s, pop %.1f, match %d' % (desc(top), top['pop'], tc)
    if rival:
        why += '; most-voted other match %s' % desc(rival)
    if tm:
        x = tm[0]
        mt = x.get('media_type') or ('movie' if path == '/search/movie' else 'tv')
        k = ('m' if mt == 'movie' else 'tv') + ':' + str(x['id'])
        why += '; TMDB search ranks %s first' % ('it' if k == top['key'] else k)
    if alts:
        why += '; equally good: ' + ', '.join(alts)
    if stronger:
        flags.append('%s | %s | %s || stronger: %s' % (cls, q, why, '; '.join(desc(p) for p in sorted(stronger, key=lambda p: -p['votes'])[:3])))
    o = {'class': cls, 'query': q}
    if types:
        o['types'] = types
    o['top1'] = top['key']
    if alts:
        o['alt'] = alts
    if len(res) > 1:
        o['top5'] = [r['key'] for r in res[1:]]
    o['why'] = why
    out.write(json.dumps(o, ensure_ascii=False) + '\n')
    n += 1
print('wrote', n, 'queries')
print('UNRESOLVED', len(unresolved))
for u in unresolved:
    print('  ', u)
print('FLAGGED', len(flags))
for f in flags:
    print('  ', f)
