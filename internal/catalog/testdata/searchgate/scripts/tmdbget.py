"""Polite read-only TMDB GET helper. Never prints the key."""
import json, time, urllib.parse, urllib.request, urllib.error

_KEY = None
_LAST = [0.0]
MIN_GAP = 0.26  # <= ~4 req/s


def _key():
    global _KEY
    if _KEY is None:
        for line in open(__import__('os').environ.get('TMDB_ENV', 'tmdb.env')):
            line = line.strip()
            if line.startswith('TMDB_KEY='):
                _KEY = line.split('=', 1)[1].strip().strip('"\'')
    return _KEY


def get(path, **params):
    k = _key()
    bearer = k.count('.') >= 2 or len(k) > 64
    q = dict(params)
    if not bearer:
        q['api_key'] = k
    url = 'https://api.themoviedb.org/3' + path + '?' + urllib.parse.urlencode(q)
    for attempt in range(6):
        gap = time.time() - _LAST[0]
        if gap < MIN_GAP:
            time.sleep(MIN_GAP - gap)
        _LAST[0] = time.time()
        req = urllib.request.Request(url, headers={'Accept': 'application/json'})
        if bearer:
            req.add_header('Authorization', 'Bearer ' + k)
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            if e.code in (429, 500, 502, 503, 504):
                time.sleep(2 * (attempt + 1))
                continue
            raise RuntimeError('tmdb %s -> HTTP %d' % (path, e.code)) from None
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            time.sleep(2 * (attempt + 1))
    raise RuntimeError('tmdb %s: gave up' % path)
