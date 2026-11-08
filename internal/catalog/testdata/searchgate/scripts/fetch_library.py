"""Fetch title/original/year/votes for J's Some id lists via /discover pages.

usage: python3 -I fetch_library.py <scriptdir> <exportsdir> <out.jsonl>
"""
import json, sys, os
sys.path.insert(0, sys.argv[1])
import tmdbget

exp, out_path = sys.argv[2], sys.argv[3]
want = {
    'movie': set(json.load(open(os.path.join(exp, 'some_movie.json')))),
    'tv': set(json.load(open(os.path.join(exp, 'some_tv.json')))),
}
got = {'movie': {}, 'tv': {}}
if os.path.exists(out_path):
    for line in open(out_path):
        r = json.loads(line)
        got[r['kind']][r['id']] = r
out = open(out_path, 'a')


def keep(kind, r):
    if r['id'] not in want[kind] or r['id'] in got[kind]:
        return
    if kind == 'movie':
        rec = dict(kind=kind, id=r['id'], title=r.get('title') or '', original=r.get('original_title') or '',
                   date=r.get('release_date') or '')
    else:
        rec = dict(kind=kind, id=r['id'], title=r.get('name') or '', original=r.get('original_name') or '',
                   date=r.get('first_air_date') or '')
    rec.update(votes=r.get('vote_count') or 0, vote_avg=r.get('vote_average') or 0, pop=r.get('popularity') or 0,
               lang=r.get('original_language') or '', poster=r.get('poster_path') or '', genres=r.get('genre_ids') or [])
    got[kind][r['id']] = rec
    out.write(json.dumps(rec, ensure_ascii=False) + '\n')


def walk(kind, year, sort, extra, stop=None):
    yk = 'primary_release_year' if kind == 'movie' else 'first_air_date_year'
    page = 1
    while page <= 500:
        params = dict(sort_by=sort, include_adult='false', language='en-US', page=page, **{yk: year}, **extra)
        if kind == 'movie':
            params['include_video'] = 'false'
        res = tmdbget.get('/discover/' + kind, **params)
        if not res:
            return
        rs = res.get('results') or []
        for r in rs:
            keep(kind, r)
        if not rs or page >= res.get('total_pages', 0) or (stop and stop(rs)):
            return
        page += 1


for kind in ('movie', 'tv'):
    for year in range(2027, 1873, -1):
        walk(kind, year, 'vote_count.desc', {'vote_count.gte': 100})
        walk(kind, year, 'popularity.desc', {}, stop=lambda rs: (rs[-1].get('popularity') or 0) < 3.5)
        out.flush()
    missing = sorted(want[kind] - set(got[kind]))
    print(kind, 'discover found', len(got[kind]), 'missing', len(missing), flush=True)
    for i in missing:
        r = tmdbget.get('/%s/%d' % (kind, i), language='en-US')
        if r:
            r['genre_ids'] = [g['id'] for g in r.get('genres') or []]
            keep(kind, r)
    out.flush()
    print(kind, 'final', len(got[kind]), 'of', len(want[kind]), flush=True)
