#!/usr/bin/env python3
"""Lab Helm post-renderer: expose private TLS endpoints to a native tailnet worker."""
import json
import os
import sys
import yaml

host = os.environ['MULTIHOST_IP']
bases = dict(server=1841, source=1842, transcode=1843, delivery=1844)
objects = list(yaml.safe_load_all(sys.stdin))
for obj in objects:
    name = obj.get('metadata', {}).get('name', '')
    role = name.rsplit('-', 1)[-1]
    if role not in bases:
        continue
    if obj['kind'] == 'ConfigMap':
        d = json.loads(obj['data']['deployment.json'])
        d['internalAddress'] = f'https://{host}:{bases[role]}__ETHER_POD_ORDINAL__'
        obj['data']['deployment.json'] = json.dumps(d)
    if obj['kind'] == 'StatefulSet':
        container = obj['spec']['template']['spec']['containers'][0]
        container['args'][0] = container['args'][0].replace('sed "s/', 'sed "s/__ETHER_POD_ORDINAL__/${POD_NAME##*-}/g;s/', 1)
        for ordinal in range(obj['spec']['replicas']):
            if ordinal > 9:
                raise ValueError('lab relay supports at most ten ordinals')
            port = bases[role]*10+ordinal
            objects.append(dict(apiVersion='v1', kind='Service', metadata=dict(name=f'{name}-{ordinal}-tailnet'), spec=dict(
                type='NodePort', selector={'statefulset.kubernetes.io/pod-name': f'{name}-{ordinal}'},
                ports=[dict(name='tls', port=32401, targetPort=32401, nodePort=port+13000)])))
yaml.safe_dump_all(objects, sys.stdout)
