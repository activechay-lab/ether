#!/usr/bin/env python3
"""Observe PSI each minute, require 30 minutes full avg10<5, then guard ONE agent.

Run on the proposed second host, not the Kubernetes server. Logs contain no
credentials. Command must create the capped agent and write STATE/node.id.
Any full avg10>10 after joining stops the captured lab container ID.
"""
import argparse
import json
import re
import signal
import subprocess
import time
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--state', required=True, type=Path)
p.add_argument('command', nargs=argparse.REMAINDER)
a = p.parse_args()
if not a.command:
    p.error('a node creation command is required')
a.state.mkdir(parents=True, exist_ok=True)
signal.signal(signal.SIGTERM, lambda *_: sys_exit())

def sys_exit():
    raise SystemExit(143)

low_since = None
cid = None
with (a.state/'pressure.jsonl').open('a', buffering=1) as out:
    try:
        while True:
            raw = Path('/proc/pressure/memory').read_text()
            full = float(re.search(r'^full avg10=([0-9.]+)', raw, re.M)[1])
            now = time.monotonic()
            low_since = (now if low_since is None else low_since) if full < 5 else None
            elapsed = 0 if low_since is None else now-low_since
            row = dict(utc=time.time(), fullAvg10=full, continuousLowSeconds=elapsed, joined=cid is not None)
            out.write(json.dumps(row)+'\n')
            print(json.dumps(row), flush=True)
            if cid and full > 10:
                break
            if cid is None and elapsed >= 1800:
                subprocess.run(a.command, check=True)
                candidate = (a.state/'node.id').read_text().strip()
                if not re.fullmatch('[a-f0-9]{64}', candidate):
                    raise RuntimeError('invalid container id')
                label = subprocess.check_output(['docker', 'inspect', '--format', '{{index .Config.Labels "ether.lab"}}', candidate], text=True).strip()
                if label != 'ether035-multihost':
                    cid = None
                    raise RuntimeError('not a lab container')
                cid = candidate
            time.sleep(60)
    finally:
        if cid:
            subprocess.run(['docker', 'stop', '--timeout', '10', cid], check=True)
