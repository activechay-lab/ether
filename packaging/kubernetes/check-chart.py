#!/usr/bin/env python3
"""Chart contract regression checks, using only rendered data (no credentials)."""
import json, os, pathlib, subprocess, tempfile
import yaml

chart = pathlib.Path(__file__).resolve().parents[1] / 'helm/ether'
helm = os.environ.get('HELM', 'helm')
base = chart.parents[1] / 'kubernetes/mixed-cpu.values.yaml'

def render(*args):
    return list(yaml.safe_load_all(subprocess.check_output([
        helm, 'template', 'contract', str(chart), '-f', str(base), *args], text=True)))

def check(docs, persistent=False):
    sts = [d for d in docs if d['kind'] == 'StatefulSet']
    assert len(sts) == 5
    for d in sts:
        s = d['spec']; pod = s['template']['spec']; c = pod['containers'][0]
        role = s['template']['metadata']['labels']['ether.plex/role']
        assert s['persistentVolumeClaimRetentionPolicy'] == dict(whenDeleted='Retain', whenScaled='Retain')
        assert s['updateStrategy']['type'] == 'OnDelete'
        assert not pod['automountServiceAccountToken']
        assert c['readinessProbe']['httpGet']['path'] == ('/admissionz' if role in ('full','server') else '/readyz')
        assert c['startupProbe']['httpGet']['path'] == '/startupz'
        assert c['livenessProbe']['httpGet']['path'] == '/livez'
        assert ('public' in [p['name'] for p in c['ports']]) == (role in ('full','server'))
        assert [v['metadata']['name'] for v in s['volumeClaimTemplates']] == (['data','cache'] if persistent else ['data'])
        assert len([v for v in pod['volumes'] if 'secret' in v]) == 3
    for d in docs:
        assert d['kind'] != 'Secret', 'chart must never render credential bytes'
        if d['kind'] == 'ConfigMap':
            config = json.loads(d['data']['deployment.json'])
            assert config['roles'] == ([] if d['metadata']['name'].endswith('-full') else [d['metadata']['name'].rsplit('-',1)[-1]])
            assert config['clusterKeyFile'] == '/run/ether/cluster/key'
            assert config['internalAddress'].startswith('https://__ETHER_POD_NAME__.')
            assert config['clockBoundKnown'] is True
            assert config['tempDir'] == '/scratch/transcode'
    public = next(d for d in docs if d['kind']=='Service' and d['metadata']['name']=='contract-ether')
    assert public['spec']['selector']['ether.plex/public'] == 'true'
    private = next(d for d in docs if d['kind']=='Service' and d['metadata']['name'].endswith('-private'))
    assert private['spec']['publishNotReadyAddresses']

common = ['--set','pools.server.replicas=2']
check(render(*common))
check(render(*common, '--set','cache.type=persistent','--set','networkPolicy.enabled=true',
             '--set','ingress.enabled=true','--set','ingress.hosts[0]=ether.example.test'), True)
check(render(*common, '-f', str(chart.parents[1]/'kubernetes/nvidia.values.yaml')))
for key,value in [('pools.typo.replicas','1'),('pools.source.bootstrap','true'),('cache.type','shared'),
                  ('runtime.capacity','0'),('service.port','0'),('secrets.database.existingSecret','')]:
    result=subprocess.run([helm,'template','invalid',str(chart),'-f',str(base),'--set',f'{key}={value}'],capture_output=True)
    assert result.returncode, f'accepted invalid {key}'
print('chart role, identity, secret, probe, policy, GPU, ingress and invalid-value contracts passed')
