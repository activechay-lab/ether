#!/usr/bin/env python3
"""Offline role-oracle checks; fixture results are never runtime acceptance."""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

root=Path(__file__).resolve().parent
image='ether035-ha:lab-fixture'
roles={'source':'source','encoder':'transcode','controller':'server','delivery':'delivery'}
members=[dict(id=axis,name=axis,incarnation=axis+'-boot',draining=0,protocol_version=1,
              last_seen=int(time.time()*1000),roles=[role]) for axis,role in roles.items()]
placement=dict(session='fixture',generation=2,controller='controller',controller_boot='controller-boot',
               node='encoder',boot='encoder-boot',role='transcode',retired=0,deadline=0)
source=dict(NodeID='source',Boot='source-boot')
proof=dict(placement=placement,route=dict(Source=source,Fence=dict(Session='fixture',Generation=2,
           Target=dict(NodeID='encoder',Boot='encoder-boot'),Controller=dict(ID='controller',Boot='controller-boot'))),
           members=members,claims=[dict(scope='encoder',units=1)],encoderRoles=['transcode'])
pods=dict(items=[dict(metadata=dict(name=axis,uid=axis+'-uid',labels={'ether.plex/role':role}),
          spec=dict(nodeName='fixture-node',containers=[dict(name='ether',image=image)]),
          status=dict(podIP='10.0.0.'+str(n+1),containerStatuses=[dict(name='ether',ready=True,
                      imageID='sha256:fixture',containerID='fixture',state=dict(running={}))]))
          for n,(axis,role) in enumerate(roles.items())])
endpoints=dict(items=[dict(endpoints=[dict(conditions=dict(ready=True),addresses=['10.0.0.4'],
                   targetRef=dict(kind='Pod',uid='delivery-uid'))])])
with tempfile.TemporaryDirectory(prefix='ether-placement-check-') as directory:
    work=Path(directory)
    stub=work/'kubectl'
    stub.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
args=sys.argv[1:]
if 'exec' in args:
    assert 'READ ONLY' in sys.stdin.read()
    name='proof'
elif 'pods' in args:name='pods'
elif 'endpointslices' in args:name='endpoints'
else:sys.exit(91)
print((Path(os.environ['FIXTURES'])/(name+'.json')).read_text())
''')
    stub.chmod(0o755)
    scenarios=['valid','no-allocation','source-fallback','wrong-boot','wrong-fence','stale-member',
               'duplicate-name','wrong-image','wrong-delivery-endpoint']
    for scenario in scenarios:
        p,ps,es=copy.deepcopy((proof,pods,endpoints))
        if scenario=='no-allocation':p['placement']=p['route']=None
        elif scenario=='source-fallback':p['members'][0]['roles']=[]
        elif scenario=='wrong-boot':p['members'][0]['incarnation']='rebooted'
        elif scenario=='wrong-fence':p['route']['Fence']['Generation']=3
        elif scenario=='stale-member':p['members'][2]['last_seen']-=60000
        elif scenario=='duplicate-name':p['members'].append(dict(p['members'][3],id='other-delivery'))
        elif scenario=='wrong-image':ps['items'][3]['spec']['containers'][0]['image']='ether035-ha:wrong'
        elif scenario=='wrong-delivery-endpoint':es['items'][0]['endpoints'][0]['targetRef']['uid']='full-fallback'
        for name,data in [('proof',p),('pods',ps),('endpoints',es)]:
            (work/(name+'.json')).write_text(json.dumps(data))
        result=subprocess.run([sys.executable,str(root/'placement-proof.py'),'--session','fixture',
            '--source-name','source','--encoder-name','encoder','--controller-name','controller',
            '--delivery-name','delivery','--delivery-service','fixture-media','--expected-image',image,
            '--strict-role-pools','--kubectl',str(stub),'--output',str(work/'output.json')],
            env={**os.environ,'FIXTURES':str(work)},capture_output=True,text=True,timeout=30)
        assert result.returncode==(0 if scenario=='valid' else 1),(scenario,result.stderr)
        record=json.loads((work/'output.json').read_text())
        assert record['allocationVerified']==(scenario=='valid'),scenario
        assert record['dedicatedRolePoolsVerified']==(scenario=='valid'),scenario
        assert record['deliveryTrafficVerified'] is False
        assert record['mediaProgressVerified'] is False
print('offline dedicated-role oracle checks passed; no runtime/media acceptance implied')
