#!/usr/bin/env python3
"""Render an isolated600s generated fixture; does not alter the180s fixture."""
import sys
import os
from pathlib import Path
import yaml

root, image = Path(sys.argv[1]), sys.argv[2]
prefix = os.environ.get("ETHER_LAB_PREFIX", "ether035-mh")
namespace = os.environ.get("ETHER_LAB_NAMESPACE", "ether035-ha")
if (namespace, prefix) not in (("ether035-ha", "ether035-mh"), ("ether035-lab2", "ether035-lab2")):
    raise SystemExit("only approved isolated lab namespace/prefix pairs are allowed")
name = namespace + "-long-source"
fixture_id = "ether035-long-fixture" if namespace == "ether035-ha" else namespace + "-long-fixture"
article_prefix = "ether035-long-part" if namespace == "ether035-ha" else namespace + "-long-part"
for obj in yaml.safe_load_all((root/'packaging/kubernetes/source-fixture.yaml').read_text()):
    obj['metadata']['name'] = name
    obj['metadata']['namespace'] = namespace
    if obj['kind'] == 'Service':
        obj['spec']['selector']['app'] = name
    if obj['kind'] == 'Deployment':
        obj['spec']['selector']['matchLabels']['app'] = name
        pod = obj['spec']['template']
        pod['metadata']['labels']['app'] = name
        spec = pod['spec']
        spec['nodeSelector'] = {'kubernetes.io/hostname':prefix+'-server'}
        gen = spec['initContainers'][0]
        gen['image'],gen['imagePullPolicy'] = image,'Never'
        gen['args'] = ['600' if arg == '180' else '/media/long-fixture.mp4' if arg == '/media/fixture.mp4' else arg for arg in gen['args']]
        spec['containers'][0]['args'] = [
            '--media','/media/long-fixture.mp4',
            '--origin','http://'+name+'.'+namespace+'.svc.cluster.local:8080',
            '--tmdb','900002','--release','Ether.Long.Cluster.Fixture.2020.360p.WEB-DL.MPEG4.AAC',
            '--fixture-id',fixture_id,'--article-prefix',article_prefix]
        # Source data/chunks/yEnc stay bounded even for the longer generated clip.
        spec['containers'][0]['resources']['limits']['memory'] = '512Mi'
        for volume in spec['volumes']:
            if volume['name'] == 'media': volume['emptyDir']['sizeLimit'] = '256Mi'
            if volume['name'] == 'code': volume['configMap']['name'] = name+'-code'
    yaml.safe_dump(obj,sys.stdout)
    print('---')
