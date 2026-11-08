"""Record TMDB search answers for every corpus query, trimmed to the fields
catalog's movieToTitle/tvToTitle read. Resumable.

usage: python3 -I record_search.py <scriptdir> <intents.txt> <out.jsonl>
Each line: {"path": "/search/multi", "query": q, "page": n, "total_pages": t, "results": [...]}
"""
import json, os, sys
sys.path.insert(0, sys.argv[1])
import tmdbget

KEEP = ['id', 'media_type', 'title', 'name', 'original_title', 'original_name', 'poster_path', 'backdrop_path',
        'release_date', 'first_air_date', 'genre_ids', 'original_language', 'origin_country', 'popularity',
        'vote_average', 'vote_count', 'adult', 'video']

intents, out_path = sys.argv[2], sys.argv[3]
done = set()
if os.path.exists(out_path):
    for line in open(out_path):
        r = json.loads(line)
        done.add((r['path'], r['query'], r['page']))
out = open(out_path, 'a')

want = []
for line in open(intents):
    line = line.strip()
    if not line or line.startswith('#'):
        continue
    parts = [p.strip() for p in line.split('|')]
    q, types = parts[1], parts[2]
    path = {'': '/search/multi', 'm': '/search/movie', 'tv': '/search/tv'}[types]
    if (path, q) not in want:
        want.append((path, q))

for path, q in want:
    pages = 3 if path == '/search/multi' else 1
    for page in range(1, pages + 1):
        if (path, q, page) in done:
            continue
        res = tmdbget.get(path, query=q, include_adult='false', language='en-US', page=page)
        if res is None:
            break
        rs = []
        for r in res.get('results') or []:
            if r.get('media_type') == 'person':
                continue
            rs.append({k: r[k] for k in KEEP if k in r})
        out.write(json.dumps(dict(path=path, query=q, page=page, total_pages=res.get('total_pages', 0), results=rs),
                             ensure_ascii=False) + '\n')
        out.flush()
        if page >= res.get('total_pages', 0):
            break
print('recorded', len(want), 'queries', flush=True)
