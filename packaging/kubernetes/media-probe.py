#!/usr/bin/env python3
"""Measure real MPEG-TS HLS delivery; never substitutes for placement/fencing proof.

Config contains manifestURL, allowedOrigins, optional headersFile, durationSeconds,
maximumGapSeconds, minimumSegments, requestTimeoutSeconds, and pace (default true).
Keep config/headers private: generated broker URLs can contain media grants.
"""
import argparse
import hashlib
import json
from pathlib import Path
import time
from urllib.error import HTTPError
from urllib.parse import urljoin, urlsplit
from urllib.request import build_opener, HTTPRedirectHandler, Request


def origin(url):
    u = urlsplit(url)
    if u.scheme not in ('http', 'https') or not u.hostname or u.username or u.password or u.fragment:
        raise ValueError('unsupported origin')
    return u.scheme, u.hostname.lower(), u.port or (443 if u.scheme == 'https' else 80)


def video_pts(data):
    """Extract real video PES presentation times from aligned 188-byte TS packets."""
    if not data or len(data) % 188:
        raise ValueError('unaligned MPEG-TS segment')
    pts = []
    for offset in range(0, len(data), 188):
        packet = data[offset:offset+188]
        if packet[0] != 0x47 or packet[1] & 0x80:
            raise ValueError('damaged MPEG-TS packet')
        if not packet[1] & 0x40:
            continue
        control = (packet[3] >> 4) & 3
        if control not in (1, 3):
            continue
        start = 4 if control == 1 else 5 + packet[4]
        pes = packet[start:]
        if len(pes) < 14 or pes[:3] != b'\x00\x00\x01' or not 0xe0 <= pes[3] <= 0xef or not pes[7] & 0x80:
            continue
        p = pes[9:14]
        if not (p[0] & p[2] & p[4] & 1):
            raise ValueError('invalid MPEG-TS presentation timestamp')
        value = ((p[0] >> 1) & 7) << 30 | p[1] << 22 | (p[2] >> 1) << 15 | p[3] << 7 | p[4] >> 1
        pts.append(value / 90000)
    if not pts:
        raise ValueError('no video presentation timestamps')
    return min(pts), max(pts)


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Do not send viewer credentials or media grants to another origin.
        return None


class Probe:
    def __init__(self, config, output):
        self.c = config
        self.output = output
        self.origins = {origin(u) for u in config['allowedOrigins']}
        self.headers = json.loads(Path(config['headersFile']).read_text()) if config.get('headersFile') else {}
        self.opener = build_opener(NoRedirect())
        self.events = (output / 'events.jsonl').open('w')
        self.started = time.monotonic()

    def event(self, **fields):
        self.events.write(json.dumps(dict(wallTime=time.time(), elapsed=time.monotonic()-self.started, **fields))+'\n')
        self.events.flush()

    def fetch(self, url, limit):
        if origin(url) not in self.origins:
            raise ValueError('unapproved media origin')
        begin = time.monotonic()
        with self.opener.open(Request(url, headers=self.headers), timeout=self.c.get('requestTimeoutSeconds', 5)) as response:
            if response.status != 200:
                raise ValueError('unsuccessful media response')
            data = response.read(limit+1)
            if len(data) > limit:
                raise ValueError('media response exceeds bound')
        return data, time.monotonic()-begin

    def playlist(self, url, depth=0):
        if depth > 3:
            raise ValueError('nested playlists exceed bound')
        data, _ = self.fetch(url, 256*1024)
        lines = data.decode('utf-8').splitlines()
        if not lines or lines[0].strip() != '#EXTM3U':
            raise ValueError('response is not HLS')
        sequence, duration, discontinuity = 0, None, False
        segments = []
        for i, line in enumerate(lines):
            line = line.strip()
            if line.startswith('#EXT-X-STREAM-INF:'):
                target = next((v.strip() for v in lines[i+1:] if v.strip() and not v.startswith('#')), '')
                return self.playlist(urljoin(url, target), depth+1)
            if line.startswith('#EXT-X-MEDIA-SEQUENCE:'):
                sequence = int(line.split(':',1)[1])
            elif line.startswith('#EXT-X-KEY:') or line.startswith('#EXT-X-MAP:'):
                raise ValueError('probe requires clear MPEG-TS HLS')
            elif line == '#EXT-X-DISCONTINUITY':
                discontinuity = True
            elif line.startswith('#EXTINF:'):
                duration = float(line.split(':',1)[1].split(',')[0])
                if not 0 < duration <= 60:
                    raise ValueError('invalid segment duration')
            elif line and not line.startswith('#') and duration is not None:
                segments.append((sequence, duration, urljoin(url,line), discontinuity))
                sequence += 1
                duration, discontinuity = None, False
        return segments

    def run(self):
        seen, count, total_bytes = set(), 0, 0
        last_sequence, last_start_pts = None, None
        last_end_pts, last_duration = None, None
        last_progress = self.started
        maximum_gap, maximum_pts_gap, failures, total_media = 0, 0, 0, 0
        deadline = self.started + self.c.get('durationSeconds', 90)
        budget = self.c.get('maximumGapSeconds', 5)
        pace = self.c.get('pace', True)
        while time.monotonic() < deadline:
            try:
                entries = self.playlist(self.c['manifestURL'])
                for sequence, duration, url, discontinuity in entries:
                    if sequence in seen or time.monotonic() >= deadline:
                        continue
                    if last_sequence is not None and sequence != last_sequence+1:
                        raise ValueError('missing HLS sequence')
                    data, latency = self.fetch(url, 16*1024*1024)
                    first_pts, end_pts = video_pts(data)
                    pts_gap = 0 if last_end_pts is None else first_pts-last_end_pts
                    if discontinuity or (last_start_pts is not None and
                       (first_pts <= last_start_pts or abs(first_pts-last_start_pts-last_duration) > 0.5)):
                        raise ValueError('video timestamp discontinuity')
                    gap = time.monotonic()-last_progress
                    maximum_gap = max(maximum_gap, gap)
                    maximum_pts_gap = max(maximum_pts_gap, pts_gap)
                    if gap > budget:
                        raise ValueError('media progress stalled beyond budget')
                    name = f'segment-{count:06d}.ts'
                    (self.output/name).write_bytes(data)
                    self.event(kind='segment', index=count, sequence=sequence, bytes=len(data),
                               sha256=hashlib.sha256(data).hexdigest(), requestSeconds=latency,
                               firstPTS=first_pts, lastPTS=end_pts, mediaDuration=duration, progressGapSeconds=gap)
                    count += 1
                    total_bytes += len(data)
                    total_media += duration
                    last_sequence, last_start_pts, last_end_pts, last_duration = sequence, first_pts, end_pts, duration
                    seen.add(sequence)
                    last_progress = time.monotonic()
                    if pace:
                        time.sleep(max(0,min(duration,deadline-time.monotonic())))
                if not entries:
                    time.sleep(0.2)
            except Exception as error:
                # Exception messages/URLs and response bodies may contain grants.
                # Record type and status only; no credentials or raw URLs.
                failures += 1
                self.event(kind='request-error', type=type(error).__name__, status=error.code if isinstance(error,HTTPError) else None)
                if isinstance(error,ValueError):
                    break
                time.sleep(0.2)
            if time.monotonic()-last_progress > budget:
                break
            # Avoid spinning when the playlist contains only already-seen entries.
            time.sleep(0.05)
        tail_gap = time.monotonic()-last_progress
        maximum_gap = max(maximum_gap,tail_gap)
        passed = count >= self.c.get('minimumSegments',10) and maximum_gap <= budget and failures == 0 and time.monotonic() >= deadline
        summary = dict(mediaProgressPassed=passed, segments=count, bytes=total_bytes, mediaSeconds=total_media,
                       elapsedSeconds=time.monotonic()-self.started, maximumProgressGapSeconds=maximum_gap,
                       maximumVideoPTSGapSeconds=maximum_pts_gap, requestErrors=failures,
                       placementAndFencingVerified=False)
        (self.output/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
        self.events.close()
        print(json.dumps(summary))
        return 0 if passed else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    config = json.loads(args.config.read_text())
    args.output.mkdir(parents=True,exist_ok=False)
    raise SystemExit(Probe(config,args.output).run())


if __name__ == '__main__':
    main()
