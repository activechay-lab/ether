#!/usr/bin/env python3
"""Pause/stop one captured owned GPU lab node, run a local verifier, restore.

Usage: remote-loss.py --container-id ID --fault pause -- VERIFIER ARGS...
Uses authenticated BB terminals; never host networking or another container.
Evidence defaults to this invoking thread's private storage.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import uuid

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--container-id', required=True)
p.add_argument('--fault', choices=['pause', 'stop'], required=True)
p.add_argument('--machine', default='gpu-workstation', choices=['gpu-workstation'])
p.add_argument('--output', type=Path)
p.add_argument('verifier', nargs=argparse.REMAINDER)
a = p.parse_args()
if not re.fullmatch('[a-f0-9]{64}', a.container_id): p.error('exact captured ID required')
if a.verifier[:1] == ['--']: a.verifier.pop(0)
if not a.verifier: p.error('local verifier argv required')
output = a.output or Path(os.environ['BB_THREAD_STORAGE'])/('remote-loss-'+uuid.uuid4().hex)
output.mkdir(mode=0o700, parents=True, exist_ok=False)
remote = r'''
import json,subprocess,signal,time,pathlib
cid=CID
fault=FAULT
fmt='{"id":"{{.Id}}","label":"{{index .Config.Labels "ether.lab"}}","network":"{{.HostConfig.NetworkMode}}","memory":{{.HostConfig.Memory}},"cpus":{{.HostConfig.NanoCpus}},"running":{{.State.Running}},"paused":{{.State.Paused}}}'
def state():return json.loads(subprocess.check_output(['docker','inspect','--format',fmt,cid],text=True))
def docker(*args):subprocess.run(['docker',*args,cid],check=True,stdout=subprocess.DEVNULL)
def pressure():return float(next(x for x in pathlib.Path('/proc/pressure/memory').read_text().splitlines() if x.startswith('full ')).split('avg10=')[1].split()[0])
def interrupted(*_):raise SystemExit(0)
signal.signal(signal.SIGINT,interrupted)
signal.signal(signal.SIGTERM,interrupted)
s=state();assert s['id']==cid and s['label']=='ether035-multihost' and s['network']!='host' and s['running'] and not s['paused']
assert s['memory']<=2*1024**3 and 0<s['cpus']<=1000000000
active=False
pressure_abort=False
try:
 active=True
 docker('pause') if fault=='pause' else docker('stop','--timeout','0')
 s=state();assert s['paused'] if fault=='pause' else not s['running']
 print('FAULT_ACTIVE '+json.dumps(dict(container=cid,fault=fault,utc=time.time())),flush=True)
 deadline=time.monotonic()+180
 while time.monotonic()<deadline:
  if pressure()>10:
   pressure_abort=True
   break
  time.sleep(1)
finally:
 if active:
  s=state()
  if s['paused']:docker('unpause')
  if pressure_abort:
   docker('stop','--timeout','10')
   print('FAULT_PRESSURE_ABORT '+cid,flush=True)
  else:
   if not s['running']:docker('start')
   s=state();assert s['running'] and not s['paused']
   print('FAULT_RESTORED '+json.dumps(dict(container=cid,running=s['running'],paused=s['paused'],utc=time.time())),flush=True)
'''.replace('CID', repr(a.container_id)).replace('FAULT\n', repr(a.fault)+'\n', 1)
payload = base64.b64encode(remote.encode()).decode()
command = "python3 -u -c 'import base64;exec(base64.b64decode(\""+payload+"\"))'"
created = json.loads(subprocess.check_output(['bb','terminal','create','--machine',a.machine,'--title','multihost-node-loss','--command',command,'--json'],text=True))
tid = created['id']
(output/'terminal.json').write_text(json.dumps(created))
def interrupted(*_):
    raise SystemExit(143)
signal.signal(signal.SIGTERM, interrupted)
code = 1
active = False
try:
    subprocess.run(['bb','terminal','wait',tid,'--contains','FAULT_ACTIVE','--from-start','--timeout','30s'],check=True)
    active = True
    code = subprocess.run(a.verifier).returncode
finally:
    # Signal only the owned terminal's foreground verifier. Its finally restores
    # the immutable container; never close a paused node without restoration.
    subprocess.run(['bb','terminal','send',tid,'--text','\x03'],check=True)
    restored = subprocess.run(['bb','terminal','wait',tid,'--contains','FAULT_RESTORED','--from-start','--timeout','40s']).returncode == 0
    logs = subprocess.run(['bb','terminal','output',tid,'--tail-bytes','16000'],capture_output=True,text=True)
    (output/'remote.log').write_text(logs.stdout)
    (output/'summary.json').write_text(json.dumps(dict(container=a.container_id,fault=a.fault,active=active,restored=restored,verifierExit=code)))
    subprocess.run(['bb','terminal','close',tid],check=True)
    if not restored: code = 1
raise SystemExit(code)
