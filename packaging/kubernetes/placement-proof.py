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
    p.add_argument('--source-name',required=True,help='Expected source cluster member name')
    p.add_argument('--encoder-name',required=True,help='Expected transcode-only cluster member name')
    p.add_argument('--controller-name')
    p.add_argument('--delivery-name')
    p.add_argument('--delivery-service')
    p.add_argument('--expected-image')
    p.add_argument('--expected-image-id',help='Independently verified Kubernetes runtime imageID/config digest')
    p.add_argument('--strict-role-pools',action='store_true',help='Require four distinct dedicated role identities, matching pods/image and singleton media endpoint')
    p.add_argument('--kubectl',default='kubectl')
    p.add_argument('--output',required=True,type=Path)
    a=p.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.-]{1,128}',a.session): p.error('invalid session identifier')
    if a.strict_role_pools and not all([a.controller_name,a.delivery_name,a.delivery_service,a.expected_image,a.expected_image_id]):
        p.error('strict role pools require controller/delivery names, delivery service, exact image and imageID')
    k=[a.kubectl,'--context','kind-ether035-ha','--request-timeout=15s','-n','ether035-ha']
    sql=f'''BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT json_build_object(
'placement',(SELECT row_to_json(x) FROM (SELECT session,generation,controller,controller_boot,node,boot,role,retired,deadline FROM placement_sessions WHERE session='{a.session}') x),
'route',(SELECT route::json FROM clusterplayback_routes WHERE session='{a.session}'),
'members',(SELECT coalesce(json_agg(x),'[]'::json) FROM (SELECT id,name,incarnation,draining,protocol_version,last_seen,caps::json->'roles' AS roles FROM cluster_nodes) x),
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
    role_proofs={}
    dedicated=False
    if a.strict_role_pools and passed:
        expected={
            'source':(a.source_name,'source'), 'encoder':(a.encoder_name,'transcode'),
            'controller':(a.controller_name,'server'), 'delivery':(a.delivery_name,'delivery')}
        members_by_name={m['name']:m for m in proof['members']}
        pod_result=subprocess.run(k+['get','pods','-o','json'],capture_output=True,text=True,timeout=20)
        if pod_result.returncode: raise SystemExit('read-only lab pod identity query failed')
        pods={pod['metadata']['name']:pod for pod in json.loads(pod_result.stdout)['items']}
        dedicated=True
        for axis,(name,role) in expected.items():
            member=members_by_name.get(name)
            pod=pods.get(name)
            if not member or not pod or sum(m['name']==name for m in proof['members'])!=1:
                dedicated=False;continue
            container=next((c for c in pod['spec']['containers'] if c['name']=='ether'),None)
            state=next((c for c in pod.get('status',{}).get('containerStatuses',[]) if c['name']=='ether'),{})
            age=time.time()*1000-member['last_seen']
            valid=bool(container and member['roles']==[role] and not member['draining']
                and member['protocol_version']>0 and -1000<=age<=30000
                and pod['metadata']['labels'].get('ether.plex/role')==role
                and container['image']==a.expected_image and state.get('ready') is True
                and state.get('imageID')==a.expected_image_id
                and 'running' in state.get('state',{}))
            role_proofs[axis]=dict(name=name,nodeID=member['id'],boot=member['incarnation'],role=role,
                podUID=pod['metadata']['uid'],image=container['image'] if container else None,
                imageID=state.get('imageID'),containerID=state.get('containerID'),podIP=pod.get('status',{}).get('podIP'),
                kubernetesNode=pod['spec']['nodeName'],lastSeenAgeMillis=age,verified=valid)
            dedicated=dedicated and valid
        if len(role_proofs)!=4 or len({r['nodeID'] for r in role_proofs.values()})!=4:
            dedicated=False
        if dedicated:
            dedicated=(role_proofs['source']['nodeID']==route['Source']['NodeID']
                and role_proofs['source']['boot']==route['Source']['Boot']
                and role_proofs['encoder']['nodeID']==placement['node']
                and role_proofs['encoder']['boot']==placement['boot']
                and role_proofs['controller']['nodeID']==placement['controller']
                and role_proofs['controller']['boot']==placement['controller_boot'])
        if dedicated:
            endpoints=subprocess.run(k+['get','endpointslices','-l',f'kubernetes.io/service-name={a.delivery_service}','-o','json'],capture_output=True,text=True,timeout=20)
            if endpoints.returncode: raise SystemExit('read-only lab media endpoint query failed')
            ready=[e for item in json.loads(endpoints.stdout)['items'] for e in item.get('endpoints',[]) if e.get('conditions',{}).get('ready') is True]
            delivery=role_proofs['delivery']
            dedicated=bool(ready) and all(e.get('targetRef',{}).get('uid')==delivery['podUID']
                and e.get('targetRef',{}).get('kind')=='Pod' and delivery['podIP'] in e.get('addresses',[]) for e in ready)
            role_proofs['delivery']['singletonServiceEndpointVerified']=dedicated
        passed=passed and dedicated
        if not passed:reason='dedicated source/encoder/controller/delivery identity or endpoint mismatch'
    # Route contains no bearer grants, but expose only the identity/fence facts
    # required for auditing and never raw SQL/config/credential material.
    record=dict(wallTime=time.time(),session=a.session,allocationVerified=passed,reason=reason,
                placement=placement,source=route['Source'] if route else None,claims=proof['claims'],
                expectedSourceName=a.source_name,expectedEncoderName=a.encoder_name,
                readOnlyEvidence=True,mediaProgressVerified=False,retirementVerified=False,
                dedicatedRolePoolsVerified=dedicated,roles=role_proofs,deliveryTrafficVerified=False)
    a.output.parent.mkdir(parents=True,exist_ok=True)
    a.output.write_text(json.dumps(record,indent=2)+'\n')
    print(json.dumps(dict(allocationVerified=passed,reason=reason,mediaProgressVerified=False,retirementVerified=False,dedicatedRolePoolsVerified=dedicated,deliveryTrafficVerified=False)))
    raise SystemExit(0 if passed else 1)


if __name__=='__main__': main()
