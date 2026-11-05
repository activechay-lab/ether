#!/usr/bin/env python3
"""Read-only exact distributed allocation oracle; neither media nor retirement proof."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import time


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--session',required=True)
    p.add_argument('--source-name',required=True,help='Expected cluster member name, normally the source full pod name')
    p.add_argument('--encoder-name',required=True,help='Expected transcode-only cluster member name')
    p.add_argument('--kubectl',default='kubectl')
    p.add_argument('--output',required=True,type=Path)
    a=p.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.-]{1,128}',a.session): p.error('invalid session identifier')
    k=[a.kubectl,'--context','kind-ether035-ha','--request-timeout=15s','-n','ether035-ha']
    sql=f'''BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT json_build_object(
'placement',(SELECT row_to_json(x) FROM (SELECT session,generation,controller,controller_boot,node,boot,role,retired,deadline FROM placement_sessions WHERE session='{a.session}') x),
'route',(SELECT route::json FROM clusterplayback_routes WHERE session='{a.session}'),
'members',(SELECT coalesce(json_agg(x),'[]'::json) FROM (SELECT id,name,incarnation,draining,protocol_version FROM cluster_nodes) x),
'claims',(SELECT coalesce(json_agg(x),'[]'::json) FROM (SELECT scope,units FROM placement_claims WHERE session='{a.session}') x),
'encoderRoles',(SELECT roles::json FROM placement_targets WHERE node=(SELECT node FROM placement_sessions WHERE session='{a.session}'))
);
COMMIT;
'''
    result=subprocess.run(k+['exec','-i','ether035-ha-pg-0','--','psql','-U','ether','-d','ether','-Atq','-v','ON_ERROR_STOP=1'],
        input=sql,text=True,capture_output=True,timeout=25)
    if result.returncode: raise SystemExit('read-only lab placement query failed')
    proof=json.loads(result.stdout)
    placement,route=proof['placement'],proof['route']
    reason='no durable distributed allocation/route'
    passed=False
    if placement and route:
        source=route['Source'];fence=route['Fence']
        members={member['id']:member for member in proof['members']}
        src=members.get(source['NodeID']);enc=members.get(placement['node'])
        passed=bool(src and enc and src['name']==a.source_name and enc['name']==a.encoder_name
            and src['incarnation']==source['Boot'] and enc['incarnation']==placement['boot']
            and source['NodeID']!=placement['node'] and not src['draining'] and not enc['draining']
            and src['protocol_version']>0 and enc['protocol_version']>0
            and placement['role']=='transcode' and placement['retired']==0 and len(proof['claims'])>0
            and proof['encoderRoles']==['transcode'] and fence['Session']==placement['session']
            and fence['Generation']==placement['generation']
            and fence['Target']==dict(NodeID=placement['node'],Boot=placement['boot'])
            and fence['Controller']==dict(ID=placement['controller'],Boot=placement['controller_boot']))
        reason='exact source/encoder/boot/fence mismatch' if not passed else 'exact durable remote allocation observed'
    # Route contains no bearer grants, but expose only the identity/fence facts
    # required for auditing and never raw SQL/config/credential material.
    record=dict(wallTime=time.time(),session=a.session,allocationVerified=passed,reason=reason,
                placement=placement,source=route['Source'] if route else None,claims=proof['claims'],
                expectedSourceName=a.source_name,expectedEncoderName=a.encoder_name,
                readOnlyEvidence=True,mediaProgressVerified=False,retirementVerified=False)
    a.output.parent.mkdir(parents=True,exist_ok=True)
    a.output.write_text(json.dumps(record,indent=2)+'\n')
    print(json.dumps(dict(allocationVerified=passed,reason=reason,mediaProgressVerified=False,retirementVerified=False)))
    raise SystemExit(0 if passed else 1)


if __name__=='__main__': main()
