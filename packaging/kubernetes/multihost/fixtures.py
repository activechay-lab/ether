#!/usr/bin/env python3
"""Render existing generated-media/Ingress fixtures into the isolated lab (PyYAML)."""
import sys
from pathlib import Path
import yaml
root, tag = Path(sys.argv[1]), sys.argv[2]
objects = []
for filename in ('source-fixture.yaml', 'ingress-lab.yaml'):
    for obj in yaml.safe_load_all((root/'packaging/kubernetes'/filename).read_text()):
        if obj.get('kind') == 'Deployment':
            spec = obj['spec']['template']['spec']
            spec['nodeSelector'] = {'kubernetes.io/hostname': 'ether035-mh-server'}
            if filename == 'source-fixture.yaml':
                spec['initContainers'][0]['image'] = 'ether035-multihost:'+tag
        objects.append(obj)
objects.extend(yaml.safe_load_all('''
apiVersion: v1
kind: Service
metadata: {name: ether035-ha-ingress}
spec:
  type: NodePort
  selector: {app: ether035-ha-ingress}
  ports: [{name: http, port: 80, targetPort: 8000, nodePort: 30080}]
---
apiVersion: v1
kind: Service
metadata: {name: ether035-ha-pg}
spec:
  type: NodePort
  selector: {app: ether035-ha-pg}
  ports: [{port: 5432, targetPort: 5432, nodePort: 30432}]
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: ether035-ha-pg}
spec:
  serviceName: ether035-ha-pg
  replicas: 1
  selector: {matchLabels: {app: ether035-ha-pg}}
  template:
    metadata: {labels: {app: ether035-ha-pg}}
    spec:
      nodeSelector: {kubernetes.io/hostname: ether035-mh-server}
      containers:
        - name: postgres
          image: postgres:16.9-alpine3.22
          args: ['-c', 'max_connections=300', '-c', 'shared_buffers=64MB']
          env:
            - {name: POSTGRES_USER, value: ether}
            - {name: POSTGRES_DB, value: ether}
            - name: POSTGRES_PASSWORD
              valueFrom: {secretKeyRef: {name: ether035-ha-pg, key: password}}
          resources:
            requests: {cpu: 50m, memory: 128Mi}
            limits: {cpu: '1', memory: 512Mi}
          readinessProbe:
            exec: {command: [pg_isready, '-U', ether]}
          volumeMounts: [{name: data, mountPath: /var/lib/postgresql/data}]
  volumeClaimTemplates:
    - metadata: {name: data}
      spec:
        accessModes: [ReadWriteOnce]
        resources: {requests: {storage: 1Gi}}
'''))
yaml.safe_dump_all(objects, sys.stdout)
