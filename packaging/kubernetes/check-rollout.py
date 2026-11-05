#!/usr/bin/env python3
"""Offline command stubs exercise identity races, not playback acceptance."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent
OLD = '11111111-1111-1111-1111-111111111111'
NEW = '22222222-2222-2222-2222-222222222222'
with tempfile.TemporaryDirectory(prefix='ether-rollout-check-') as directory:
    work = Path(directory)
    kubectl = work / 'kubectl'
    kubectl.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
args=sys.argv[1:]
with open(os.environ['CALLS'],'a') as f: f.write(json.dumps(args)+'\\n')
mode=os.environ['MODE']
old=os.environ['OLD']; new=os.environ['NEW']
if 'get' in args:
    print('full\\t'+old+'\\t'+('/config' if mode=='legacy' else '/wrong' if mode=='unknown-layout' else '/config/ether') if 'role' in ' '.join(args) else new)
elif 'exec' in args:
    assert args[args.index('--data-dir')+1] == ('/config' if mode=='legacy' else '/config/ether')
    if mode=='drain-fails': sys.exit(23)
elif 'delete' in args:
    assert '--raw' in args and '-f' in args and args[-1]=='-'
    assert args[args.index('--raw')+1]=='/api/v1/namespaces/ether035-ha/pods/test-full-0'
    body=json.load(sys.stdin)
    assert body=={'apiVersion':'v1','kind':'DeleteOptions','preconditions':{'uid':old}}
    # Simulate replacement AFTER drain, while the request is in flight.
    if mode=='replaced':
        print('Conflict: UID precondition failed',file=sys.stderr)
        sys.exit(41)
elif 'wait' not in args:
    sys.exit(90)
''')
    kubectl.chmod(0o755)
    for mode, expected in [('drain-fails',23),('replaced',41),('success',0),('legacy',0),('unknown-layout',64)]:
        calls = work / f'{mode}.jsonl'
        result = subprocess.run(['bash', str(ROOT/'rollout.sh'), 'ether035-ha','test-full-0'],
            env={**os.environ, 'KUBECTL':str(kubectl),'MODE':mode,'CALLS':str(calls),'OLD':OLD,'NEW':NEW},
            capture_output=True,text=True,timeout=10)
        assert result.returncode==expected, (mode,result.returncode,result.stderr)
        commands=[json.loads(line) for line in calls.read_text().splitlines()]
        deletes=[command for command in commands if 'delete' in command]
        assert len(deletes)==(0 if mode in ('drain-fails','unknown-layout') else 1)
        if mode=='replaced':
            assert 'Conflict' in result.stderr
            assert not any('wait' in command for command in commands)

    docker = work / 'docker'
    container_id = 'a'*64
    docker.write_text('''#!/usr/bin/env python3
import json,os,sys
args=sys.argv[1:]; identity='a'*64
with open(os.environ['DOCKER_CALLS'],'a') as f: f.write(json.dumps(args)+'\\n')
if args[0]=='inspect' and args[-1]=='ether035-ha-worker':
    # A reused name would point at another object on a second name inspection.
    assert '.Id' in args[2] and 'kind.cluster' in args[2] and 'kind.role' in args[2]
    print(identity+' ether035-ha worker')
else:
    assert args[-1]==identity, args
    if args[0]=='inspect':
        print('false/0' if args[2]=='{{.State.Running}}/{{.State.Pid}}' else 'id='+identity+' running=false pid=0')
''')
    docker.chmod(0o755)
    verifier=work/'verifier'
    verifier.write_text('#!/bin/sh\nexit 0\n')
    verifier.chmod(0o755)
    calls=work/'loss-kubectl.jsonl'
    docker_calls=work/'docker.jsonl'
    result=subprocess.run(['bash',str(ROOT/'hard-loss.sh'),'ether035-ha','ether035-ha-worker',str(verifier),str(work/'evidence')],
        env={**os.environ,'PATH':str(work)+os.pathsep+os.environ['PATH'],'KUBECTL':str(kubectl),'MODE':'success',
             'CALLS':str(calls),'DOCKER_CALLS':str(docker_calls),'OLD':OLD,'NEW':NEW},capture_output=True,text=True,timeout=10)
    assert result.returncode==0,(result.returncode,result.stderr)
    commands=[json.loads(line) for line in docker_calls.read_text().splitlines()]
    assert sum(command[-1]=='ether035-ha-worker' for command in commands)==1
    assert next(command for command in commands if command[0]=='stop')[-1]==container_id
    assert next(command for command in commands if command[0]=='start')[-1]==container_id
print('offline rollout checks passed: drain failure, UID conflict, exact-object delete and container identity')
