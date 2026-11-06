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
        assert next(e for e in c['env'] if e['name']=='ETHER_DATA_DIR')['value']=='/config/ether'
        assert 'explicit migration required' in c['args'][0]
        assert 'chmod 700 /config/ether' in c['args'][0]
        assert c['readinessProbe']['httpGet']['path'] == ('/admissionz' if role in ('full','server') else '/readyz')
        assert c['startupProbe']['httpGet']['path'] == '/startupz'
        assert c['livenessProbe']['httpGet']['path'] == '/livez'
        assert ('public' in [p['name'] for p in c['ports']]) == (role in ('full','server','delivery'))
        assert s['template']['metadata']['labels']['ether.plex/public'] == str(role in ('full','server')).lower()
        assert s['template']['metadata']['labels']['ether.plex/media'] == str(role in ('full','delivery')).lower()
        assert [v['metadata']['name'] for v in s['volumeClaimTemplates']] == (['data','cache'] if persistent else ['data'])
        assert len([v for v in pod['volumes'] if 'secret' in v]) == 3
        if 'nvidia.com/gpu' in c['resources'].get('limits',{}):
            assert next(e for e in c['env'] if e['name']=='NVIDIA_DRIVER_CAPABILITIES')['value']=='compute,video,utility'
    for d in docs:
        assert d['kind'] != 'Secret', 'chart must never render credential bytes'
        if d['kind'] == 'ConfigMap':
            config = json.loads(d['data']['deployment.json'])
            assert config['roles'] == ([] if d['metadata']['name'].endswith('-full') else [d['metadata']['name'].rsplit('-',1)[-1]])
            assert config['clusterKeyFile'] == '/run/ether/cluster/key'
            assert config['internalAddress'].startswith('https://__ETHER_POD_NAME__.')
            assert config['clockBoundKnown'] is True
            assert config['ledgerCapacity'] == 1000000
            assert config['tempDir'] == '/scratch/transcode'
    public = next(d for d in docs if d['kind']=='Service' and d['metadata']['name']=='contract-ether')
    assert public['spec']['selector']['ether.plex/public'] == 'true'
    media = next(d for d in docs if d['kind']=='Service' and d['metadata']['name']=='contract-ether-media')
    assert media['spec']['type']=='ClusterIP'
    assert media['spec']['selector']['ether.plex/media']=='true'
    for service,roles in [(public,{'full','server'}),(media,{'full','delivery'})]:
        selector=service['spec']['selector']
        matches={d['spec']['template']['metadata']['labels']['ether.plex/role'] for d in sts
                 if all(d['spec']['template']['metadata']['labels'].get(k)==v for k,v in selector.items())}
        assert matches==roles,(service['metadata']['name'],matches)
    ingress=next(d for d in docs if d['kind']=='Ingress')
    paths=ingress['spec']['rules'][0]['http']['paths']
    assert {p['path'] for p in paths}=={'/','/sessions/media','/video/:/transcode/universal/session','/jellyfin/sessions/media','/emby/sessions/media'}
    def backend(path):
        # Kubernetes Prefix matches whole path elements; longer prefix wins.
        matches=[p for p in paths if p['path']=='/' or path==p['path'] or path.startswith(p['path']+'/')]
        return max(matches,key=lambda p:len(p['path']))['backend']['service']['name']
    for path in ['/sessions/media/id/grant/file','/sessions/media/id/grant/master.m3u8',
                 '/video/:/transcode/universal/session/id/base/00001.ts',
                 '/jellyfin/sessions/media/id/grant/file','/emby/sessions/media/id/grant/file',
                 '/jellyfin/sessions/media/id/grant/master.m3u8','/emby/sessions/media/id/grant/base/0.ts']:
        assert backend(path)=='contract-ether-media'
    # Non-authored boundary probes still hit the Prefix and may correctly404.
    # Do not treat these as writer-derived compatibility defects or API paths.
    for path in ['/video/:/transcode/universal/session/id/subtitles/1.ass',
                 '/video/:/transcode/universal/session/id/base/index.mpd']:
        assert backend(path)=='contract-ether-media'
    for path in ['/video/:/transcode/universal/decision','/video/:/transcode/universal/start.m3u8',
                 '/video/:/transcode/universal/start.mpd','/video/:/transcode/universal/start',
                 '/video/:/transcode/universal/start.mkv','/video/:/transcode/universal/ping',
                 '/video/:/transcode/universal/stop','/transcode/sessions',
                 '/video/:/transcode/universal/subtitles','/subtitles/:/transcode/universal/start',
                 '/library/parts/1/file.mp4','/library/parts/1','/library/streams/1','/downloadQueue/1',
                 '/jellyfin/Videos/id/master.m3u8','/emby/Videos/id/master.m3u8',
                 '/jellyfin/sessions/mediaevil/id','/emby/sessions/mediaevil/id',
                 '/silo/sessions/media/id/grant/file','/sessions/mediaevil/id',
                 '/video/:/transcode/universal/sessionevil/id/base/00001.ts']:
        assert backend(path)=='contract-ether',path

    private = next(d for d in docs if d['kind']=='Service' and d['metadata']['name'].endswith('-private'))
    assert private['spec']['publishNotReadyAddresses']

# Exercise the startup guard before any server executes or identity is created.
startup = next(d for d in render() if d['kind']=='StatefulSet')['spec']['template']['spec']['containers'][0]['args'][0]
initialization = startup[:startup.index('sed ')]
with tempfile.TemporaryDirectory(prefix='ether-pvc-layout-') as directory:
    path = pathlib.Path(directory)
    (path/'node.json').write_text('legacy identity sentinel')
    script = initialization.replace('/config', directory)
    refused = subprocess.run(['/bin/sh','-ec',script], capture_output=True)
    assert refused.returncode == 64 and not (path/'ether').exists()
    assert (path/'node.json').read_text() == 'legacy identity sentinel'
    (path/'node.json').unlink()
    subprocess.run(['/bin/sh','-ec',script],check=True)
    assert (path/'ether').stat().st_mode & 0o777 == 0o700

common = ['--set','pools.server.replicas=2']
check(render(*common))
check(render(*common, '--set','cache.type=persistent','--set','networkPolicy.enabled=true',
             '--set','ingress.enabled=true','--set','ingress.hosts[0]=ether.example.test'), True)
check(render(*common, '-f', str(chart.parents[1]/'kubernetes/nvidia.values.yaml')))
# Full-only retains the original single L4 service and catch-all Ingress.
for kind in ['ClusterIP','LoadBalancer','NodePort']:
    full=render('--set','pools.full.replicas=1','--set','pools.server.replicas=0',
        '--set','pools.source.replicas=0','--set','pools.transcode.replicas=0',
        '--set','pools.delivery.replicas=0','--set',f'service.type={kind}','--set','ingress.enabled=false')
    assert {d['metadata']['name'] for d in full if d['kind']=='Service'}=={'contract-ether','contract-ether-private'}
    assert not any(d['kind']=='Ingress' for d in full)
full_ingress=render('--set','pools.server.replicas=0','--set','pools.delivery.replicas=0')
assert [p['path'] for d in full_ingress if d['kind']=='Ingress' for p in d['spec']['rules'][0]['http']['paths']]==['/']
for flags,expected in [
    (['--set','ingress.canonicalHLSLabOnly=false'],'canonical HLS LAB ONLY'),
    (['--set','ingress.enabled=false'],'require ingress.enabled=true'),
    (['--set','pools.server.replicas=1','--set','service.type=LoadBalancer'],'service.type=ClusterIP'),
    (['--set','pools.server.replicas=1','--set','service.type=NodePort'],'service.type=ClusterIP'),
    (['--set','pools.full.replicas=0','--set','pools.server.replicas=0'],'require an API pool'),
    (['--set','pools.full.replicas=0','--set','pools.server.replicas=1','--set','pools.delivery.replicas=0'],'require a media pool'),
    (['--set-json','ingress.hosts=[]'],'ingress.hosts must be nonempty')]:
    invalid=subprocess.run([helm,'template','invalid',str(chart),'-f',str(base),*flags],capture_output=True,text=True)
    assert invalid.returncode and expected in invalid.stderr,(flags,invalid.stderr)

for key,value in [('pools.typo.replicas','1'),('pools.source.bootstrap','true'),('cache.type','shared'),
                  ('runtime.capacity','0'),('runtime.ledgerCapacity','4095'),('runtime.ledgerCapacity','10000001'),('runtime.ledgerCapacity','1.5'),('pools.full.ledgerCapacity','4096'),('service.port','0'),('secrets.database.existingSecret','')]:
    result=subprocess.run([helm,'template','invalid',str(chart),'-f',str(base),'--set',f'{key}={value}'],capture_output=True)
    assert result.returncode, f'accepted invalid {key}'
print('chart role, identity, secret, probe, policy, GPU, ingress and invalid-value contracts passed')
