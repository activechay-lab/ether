#!/usr/bin/env python3
"""Lab-only, immutable generated profiles behind the existing fixture transport.

This module does not install resources or seed a database. The original fixture
remains the default; extra files are validated and served without caching them.
"""
import argparse
import binascii
from collections import Counter
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import threading
from urllib.parse import urlsplit

_BASE = Path(__file__).with_name('source-fixture.py')
if not _BASE.exists():
    _BASE = Path(__file__).parent.parent / 'source-fixture.py'
_spec = importlib.util.spec_from_file_location('ether_source_fixture', _BASE)
base = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(base)
CHUNK = 256 * 1024
PROFILES = {'ramp96': 900003, 'ac3-96': 900004, 'copy96': 900005}
NAMESPACES = {'ether035-ha', 'ether035-lab2'}


def read_manifest(path, namespace):
    value = json.loads(path.read_text())
    if namespace not in NAMESPACES or value.get('namespace') != namespace or value.get('version') != 1:
        raise ValueError('unapproved fixture manifest namespace/version')
    profiles = value.get('profiles', [])
    if len(profiles) != 3 or {p['id'] for p in profiles} != set(PROFILES):
        raise ValueError('manifest must contain exactly the three generated modes')
    for p in profiles:
        slug, tmdb = p['id'], p['tmdb']
        if (tmdb != PROFILES[slug] or str(p['ratingKey']) != str(10**15 + tmdb)
                or p['fixtureId'] != namespace+'-'+slug+'-fixture'
                or p['articlePrefix'] != namespace+'-'+slug+'-part'
                or Path(p['file']).name != p['file'] or p['file'] in ('', '.', '..')
                or not re.fullmatch(r'[a-f0-9]{64}', p['sha256'])
                or type(p['bytes']) is not int or not 0 < p['bytes'] <= 32*1024*1024
                or not isinstance(p['title'], str) or not 0 < len(p['title']) <= 128
                or any(ord(c) < 32 for c in p['title'])
                or not isinstance(p['release'], str) or not 0 < len(p['release']) <= 256
                or any(ord(c) < 32 for c in p['release'])):
            raise ValueError('invalid or nonisolated generated profile')
    if len({p['file'] for p in profiles}) != 3:
        raise ValueError('duplicate media path')
    return value


class Articles:
    """Resolve a single exact Message-ID; never cache encoded file contents."""
    def __init__(self, fixture):
        self.fixture = fixture

    def get(self, article_id):
        f = self.fixture
        match = re.fullmatch(re.escape(f.article_prefix)+r'-(\d{5})@fixture\.invalid', article_id)
        if not match or not 1 <= int(match[1]) <= f.parts:
            return None
        return f.encode(int(match[1])-1)


class FileFixture(base.Fixture):
    def __init__(self, root, origin, profile):
        self.path = root / profile['file']
        original = self.path.lstat()
        if (not stat.S_ISREG(original.st_mode) or self.path.resolve().parent != root.resolve()
                or original.st_size != profile['bytes'] or not 0 < original.st_size <= 32*1024*1024):
            raise ValueError('fixture must be a regular file directly inside media root')
        digest, crc = hashlib.sha256(), 0
        with self.path.open('rb') as stream:
            if self.identity(original) != self.identity(os.fstat(stream.fileno())):
                raise ValueError('fixture replaced during open')
            while block := stream.read(CHUNK):
                digest.update(block)
                crc = binascii.crc32(block, crc)
        if (original.st_size != profile['bytes'] or digest.hexdigest() != profile['sha256']
                or self.identity(original) != self.identity(self.path.stat())):
            raise ValueError('fixture artifact changed or hash/size mismatch')
        self.stat_identity = self.identity(original)
        self.profile = profile
        self.file_crc, self.sha256 = crc & 0xffffffff, digest.hexdigest()
        self._size = original.st_size
        self.filename = self.path.name
        self.fixture_id, self.article_prefix = profile['fixtureId'], profile['articlePrefix']
        self.origin, self.tmdb, self.release = origin.rstrip('/'), profile['tmdb'], profile['release']
        self.stats, self.lock = Counter(), threading.Lock()
        self.articles = Articles(self)

    @staticmethod
    def identity(value):
        return value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns

    @property
    def size(self):
        return self._size

    @property
    def parts(self):
        return (self.size + CHUNK-1)//CHUNK

    def chunk_size(self, index):
        if not 0 <= index < self.parts:
            raise ValueError('invalid article index')
        return min(CHUNK, self.size-index*CHUNK)

    def open(self):
        stream = self.path.open('rb')
        if self.identity(os.fstat(stream.fileno())) != self.stat_identity:
            stream.close()
            raise ValueError('immutable fixture was changed')
        return stream

    def encode(self, index):
        count = self.chunk_size(index)
        with self.open() as stream:
            stream.seek(index*CHUNK)
            data = stream.read(count)
        if len(data) != count:
            raise ValueError('short fixture read')
        return base.encode_part(data, index, self.parts, self.size, self.filename, self.file_crc)


class Registry:
    def __init__(self, primary, profiles):
        self.primary = primary
        self.by_id = {f.fixture_id: f for f in profiles}
        self.by_tmdb = {str(f.tmdb): f for f in profiles}
        self.by_slug = {f.profile['id']: f for f in profiles}
        self.article_prefixes = {f.article_prefix+'-': f for f in profiles}
        if (primary.fixture_id in self.by_id or str(primary.tmdb) in self.by_tmdb
                or primary.article_prefix+'-' in self.article_prefixes):
            raise ValueError('primary identity overlaps additional profile')

    def request_fixture(self, query):
        if query.get('t') == ['get'] and len(query.get('id', [])) == 1:
            return self.by_id.get(query['id'][0], self.primary)
        if len(query.get('tmdbid', [])) == 1:
            return self.by_tmdb.get(query['tmdbid'][0], self.primary)
        return self.primary

    def article_fixture(self, article_id):
        for prefix, fixture in self.article_prefixes.items():
            if article_id.startswith(prefix):
                return fixture
        return self.primary


def byte_range(value, size):
    if value is None:
        return 0, size-1, False
    match = re.fullmatch(r'bytes=(\d{0,12})-(\d{0,12})', value)
    if not match or not any(match.groups()):
        raise ValueError('only one byte range is supported')
    first, last = match.groups()
    if first:
        start = int(first)
        end = min(int(last), size-1) if last else size-1
        if start >= size or end < start:
            raise ValueError('unsatisfiable byte range')
    else:
        length = int(last)
        if length == 0:
            raise ValueError('empty suffix')
        start, end = max(0, size-length), size-1
    return start, end, True


class HTTP(base.HTTP):
    def fixture_for_request(self, parsed, query):
        return self.server.registry.request_fixture(query)

    def do_GET(self):
        if urlsplit(self.path).path.startswith('/media/'):
            self.media(False)
        else:
            super().do_GET()

    def do_HEAD(self):
        if urlsplit(self.path).path.startswith('/media/'):
            self.media(True)
        else:
            self.send_error(501)

    def media(self, head):
        fixture = self.server.registry.by_slug.get(urlsplit(self.path).path.removeprefix('/media/'))
        if fixture is None:
            self.send_error(404)
            return
        try:
            if not head and len(self.headers.get_all('Range', [])) > 1:
                raise ValueError('multiple range headers')
            start, end, partial = byte_range(None if head else self.headers.get('Range'), fixture.size)
        except ValueError:
            self.send_response(416)
            self.send_header('Content-Range', 'bytes */'+str(fixture.size))
            self.send_header('Content-Length', '0')
            self.end_headers()
            return
        try:
            stream = fixture.open()
        except (OSError, ValueError):
            self.send_error(503)
            return
        with stream:
            self.send_response(206 if partial else 200)
            self.send_header('Content-Type', 'application/octet-stream')
            self.send_header('Accept-Ranges', 'bytes')
            self.send_header('ETag', '"'+fixture.sha256+'"')
            self.send_header('Content-Length', str(end-start+1))
            if partial:
                self.send_header('Content-Range', f'bytes {start}-{end}/{fixture.size}')
            self.end_headers()
            if head:
                return
            stream.seek(start)
            remaining = end-start+1
            try:
                while remaining:
                    block = stream.read(min(64*1024, remaining))
                    if not block:
                        return
                    self.wfile.write(block)
                    remaining -= len(block)
            except (BrokenPipeError, ConnectionResetError, TimeoutError):
                return


class NNTP(base.NNTP):
    def fixture_for_article(self, article_id):
        return self.server.registry.article_fixture(article_id)


class Bounded:
    daemon_threads = True

    def process_request(self, request, address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        request.settimeout(15)
        try:
            super().process_request(request, address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, address):
        try:
            super().process_request_thread(request, address)
        finally:
            self.slots.release()


class Web(Bounded, http.server.ThreadingHTTPServer):
    def __init__(self, address, registry):
        self.slots = threading.BoundedSemaphore(4)
        self.registry, self.fixture = registry, registry.primary
        super().__init__(address, HTTP)


class News(Bounded, base.TCP):
    def __init__(self, address, registry):
        self.slots = threading.BoundedSemaphore(4)
        self.registry, self.fixture = registry, registry.primary
        super().__init__(address, NNTP)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--media', type=Path, required=True)
    p.add_argument('--origin', required=True)
    p.add_argument('--profiles', type=Path, required=True)
    p.add_argument('--namespace', choices=sorted(NAMESPACES), required=True)
    p.add_argument('--tmdb', type=int, default=900001)
    p.add_argument('--release', default='Ether.Cluster.Fixture.2020.360p.WEB-DL.MPEG4.AAC')
    p.add_argument('--fixture-id', default='ether035-fixture')
    p.add_argument('--article-prefix', default='ether035-ha-part')
    a = p.parse_args()
    origin = 'http://'+a.namespace+'-source.'+a.namespace+'.svc.cluster.local:8080'
    if a.origin != origin or a.tmdb != 900001:
        p.error('only the approved existing primary fixture endpoint is allowed')
    manifest = read_manifest(a.profiles, a.namespace)
    primary = base.Fixture(a.media, a.origin, a.tmdb, a.release, a.fixture_id, a.article_prefix)
    registry = Registry(primary, [FileFixture(a.media.parent, a.origin, f) for f in manifest['profiles']])
    web, news = Web(('0.0.0.0', 8080), registry), News(('0.0.0.0', 1190), registry)
    thread = threading.Thread(target=news.serve_forever)
    thread.start()
    print(json.dumps({'fixture':'generated-modes', 'profiles':sorted(registry.by_slug)}), flush=True)
    try:
        web.serve_forever()
    finally:
        web.server_close()
        news.shutdown()
        news.server_close()
        thread.join()


if __name__ == '__main__':
    main()
