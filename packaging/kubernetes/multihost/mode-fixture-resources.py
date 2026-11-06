#!/usr/bin/env python3
"""Render review-only mode fixture resources and a byte-preserving rollback.

Reads a captured lab fixture Deployment and a verified artifact manifest. It
never calls kubectl, alters the cluster, copies media or generates media.
"""
import argparse
import copy
import importlib.util
import json
from pathlib import Path

_spec = importlib.util.spec_from_file_location('ether_mode_fixture', Path(__file__).with_name('mode-fixture.py'))
mode = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mode)
MIB = 1024*1024


def render(deployment, manifest, old_size):
    ns = manifest['namespace']
    meta, spec = deployment['metadata'], deployment['spec']
    pod = spec['template']['spec']
    if (meta['namespace'] != ns or meta['name'] != ns+'-source' or not meta.get('uid')
            or spec['replicas'] != 1 or len(pod['containers']) != 1
            or pod['containers'][0]['name'] != 'source'
            or {v['name'] for v in pod['volumes']} != {'code', 'media'}
            or next(v for v in pod['volumes'] if v['name'] == 'code')['configMap']['name'] != ns+'-source-code'
            or 'emptyDir' not in next(v for v in pod['volumes'] if v['name'] == 'media')
            or [v['name'] for v in pod.get('initContainers', [])] != ['generate']
            or old_size <= 0):
        raise ValueError('unexpected original fixture Deployment; operator must review')
    container = pod['containers'][0]
    origin = 'http://'+ns+'-source.'+ns+'.svc.cluster.local:8080'
    if container['args'] != ['--media', '/media/fixture.mp4', '--origin', origin]:
        raise ValueError('unexpected primary transport arguments')
    files = old_size+sum(p['bytes'] for p in manifest['profiles'])
    if files+16*MIB > 128*MIB:
        raise ValueError('128Mi PVC lacks the required16Mi file/metadata margin')
    name = ns+'-mode-source'
    rollback = copy.deepcopy(spec['template'])
    rollback['spec'].pop('initContainers')
    media = next(v for v in rollback['spec']['volumes'] if v['name'] == 'media')
    media.pop('emptyDir')
    media['persistentVolumeClaim'] = {'claimName': name+'-media', 'readOnly': True}
    target = copy.deepcopy(rollback)
    next(v for v in target['spec']['volumes'] if v['name'] == 'code')['configMap']['name'] = name+'-code'
    target['spec']['containers'][0]['command'] = ['python3', '-B', '/code/mode-fixture.py']
    target['spec']['containers'][0]['args'] += ['--profiles', '/code/profiles.json', '--namespace', ns]
    resource = lambda kind, suffix: {'apiVersion':'v1', 'kind':kind, 'metadata':{'name':name+suffix, 'namespace':ns}}
    cm = resource('ConfigMap', '-code')
    cm['immutable'] = True
    cm['data'] = {'profiles.json':json.dumps(manifest, indent=2)+'\n',
                  'mode-fixture.py':Path(__file__).with_name('mode-fixture.py').read_text(),
                  'source-fixture.py':mode._BASE.read_text()}
    pvc = resource('PersistentVolumeClaim', '-media')
    pvc['spec'] = {'accessModes':['ReadWriteOnce'], 'resources':{'requests':{'storage':'128Mi'}}}
    upload = resource('Pod', '-upload')
    upload['spec'] = {'automountServiceAccountToken':False, 'restartPolicy':'Never',
                      'activeDeadlineSeconds':120, 'nodeSelector':pod['nodeSelector'],
                      'tolerations':pod.get('tolerations', []),
                      'securityContext':pod['securityContext'],
                      'containers':[{'name':'upload', 'image':container['image'], 'imagePullPolicy':'Never',
                                     'command':['python3', '-B', '-c', 'import time; time.sleep(115)'],
                                     'resources':{'requests':{'cpu':'10m', 'memory':'16Mi'}, 'limits':{'cpu':'100m', 'memory':'64Mi'}},
                                     'securityContext':container['securityContext'],
                                     'volumeMounts':[{'name':'media', 'mountPath':'/media'}]}],
                      'volumes':[{'name':'media', 'persistentVolumeClaim':{'claimName':name+'-media'}}]}
    return {'state':'REVIEW_ONLY_NOT_APPLIED', 'namespace':ns, 'deploymentUID':meta['uid'],
            'capacity':{'oldMediaBytes':old_size, 'newMediaBytes':files-old_size, 'allFileBytes':files,
                        'metadataMarginBytes':16*MIB, 'requestedCapacityBytes':128*MIB,
                        'rootPeakAdditionalBytesOneLab':files+16*MIB,
                        'rootSteadyAdditionalBytesOneLab':files-old_size+16*MIB,
                        'rootBothLabsStagingBytesWithoutCleanupCredit':2*(files+16*MIB),
                        'note':'No old-media cleanup credit is required for staging. PVC request is not evidence of disk allocation. Post-roll operator must independently measure40GB guard before seed; build allowance does not authorize this extra allocation.'},
            'resources':[pvc, cm, upload],
            'deploymentJSONPatch':[{'op':'test', 'path':'/metadata/uid', 'value':meta['uid']},
                                   {'op':'test', 'path':'/metadata/resourceVersion', 'value':meta['resourceVersion']},
                                   {'op':'test', 'path':'/spec/template', 'value':spec['template']},
                                   {'op':'replace', 'path':'/spec/template', 'value':target}],
            'rollbackPodTemplate':rollback,
            'rollbackRule':'Re-read current UID/resourceVersion and test installed target before replacing only template. Rollback keeps retained-byte PVC and original codeCM; generate init is absent. No original media regeneration or deletion.'}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--deployment', required=True, type=Path)
    p.add_argument('--manifest', required=True, type=Path)
    p.add_argument('--namespace', choices=sorted(mode.NAMESPACES), required=True)
    p.add_argument('--context', choices=['ether035-multihost', 'ether035-lab2'], required=True)
    p.add_argument('--old-media-bytes', type=int, required=True)
    a = p.parse_args()
    if (a.namespace, a.context) not in [('ether035-ha','ether035-multihost'), ('ether035-lab2','ether035-lab2')]:
        p.error('unapproved lab namespace/context tuple')
    result = render(json.loads(a.deployment.read_text()), mode.read_manifest(a.manifest, a.namespace), a.old_media_bytes)
    result['context'] = a.context
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
