#!/usr/bin/env python3
"""Seed an isolated generated-media lab via shipping owner APIs + fixture-only SQL.

No external account claim. Credentials stay in the supplied private directory.
Requires a loopback port-forward to a full/server node or the lab Ingress,
and the named lab PostgreSQL pod.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


def save(path, text):
    fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
    os.fchmod(fd,0o600)
    with os.fdopen(fd,'w') as out: out.write(text)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base',required=True)
    p.add_argument('--host',choices=['ether035-roles.test'],help='Approved lab Ingress Host header')
    p.add_argument('--namespace',default='ether035-ha')
    p.add_argument('--context', choices=['kind-ether035-ha', 'ether035-multihost'], default='kind-ether035-ha')
    p.add_argument('--kubectl',default='kubectl')
    p.add_argument('--credentials-dir',required=True,type=Path)
    a=p.parse_args()
    if a.namespace != 'ether035-ha' or urlsplit(a.base).hostname not in ('127.0.0.1','localhost'):
        p.error('only ether035-ha namespace and loopback API origin are allowed')
    k=[a.kubectl,'--context',a.context,'--request-timeout=15s','-n',a.namespace]
    pg=json.loads(subprocess.check_output(k+['get','pod','ether035-ha-pg-0','-o','json'],timeout=20))
    if pg['metadata']['labels'].get('app')!='ether035-ha-pg':
        p.error('PostgreSQL pod is not the lab fixture')
    a.credentials_dir.mkdir(parents=True,exist_ok=True)
    a.credentials_dir.chmod(0o700)
    credentials_path=a.credentials_dir/'local-owner.json'
    existing=credentials_path.exists()
    credentials=json.loads(credentials_path.read_text()) if existing else dict(username='ether035-lab-owner',password=secrets.token_urlsafe(24))
    if credentials['username']!='ether035-lab-owner': p.error('unexpected local viewer')
    headers={'Content-Type':'application/json','Origin':a.base,'X-Ether-Request':'1'}
    if a.host:
        headers['Host']=a.host
        headers['Origin']=urlsplit(a.base).scheme+'://'+a.host
    def call(path, method='GET', value=None):
        with urlopen(Request(a.base+path,headers=headers,method=method,
                             data=None if value is None else json.dumps(value).encode()),timeout=30) as response:
            return json.load(response)
    try:
        login=call('/ether/api/auth/local' if existing else '/ether/api/setup/local','POST',credentials)
        save(credentials_path,json.dumps(credentials))
        save(a.credentials_dir/'admin-token',login['token'])
        headers['X-Plex-Token']=login['token']
        config=call('/ether/api/config')['config']
        if config['plex'].get('claimed'):
            p.error('refusing a claimed server')
        if any(v['id']!='ether035-ha-source' for v in (config['usenet']['providers'] or [])) or any(v['id']!='ether035-ha-source' for v in (config['indexers'] or [])):
            p.error('refusing to replace nonfixture sources')
        config['usenet']['providers']=[dict(id='ether035-ha-source',name='Ether generated fixture',
            host='ether035-ha-source.ether035-ha.svc.cluster.local',port=1190,tls=False,
            username='',password='',connections=4,enabled=True,priority=0)]
        config['usenet']['minIdle']=1
        config['indexers']=[dict(id='ether035-ha-source',name='Ether generated fixture',
            url='http://ether035-ha-source.ether035-ha.svc.cluster.local:8080',apiKey='',priority=0,
            enabled=True,protocol='usenet',movieCategories=[2000])]
        call('/ether/api/config','PUT',config)
    except Exception as error:
        # HTTP exception strings/response bodies can contain credential material.
        raise SystemExit('lab owner/config API failed: '+type(error).__name__)
    token='ether-cast-'+secrets.token_urlsafe(24)
    save(a.credentials_dir/'playback-token',token)
    save(a.credentials_dir/'playback-headers.json',json.dumps({
        'X-Plex-Token':token,'X-Plex-Client-Identifier':'ether035-ha-generated-client',
        'X-Plex-Product':'Plex Web','X-Plex-Platform':'Chrome'}))
    now=int(time.time())
    # Mirrors the current detailed catalog fixture columns. No generated columns,
    # identity cloning, raw upstream account token, or production DB URL involved.
    sql=f'''BEGIN;
INSERT INTO catalog_titles(key,type,tmdb_id,title,original_title,sort_title,year,released_ts,runtime_sec,language,content_rating,content_rating_rank,browsable,added_at,fetched_at,detail,detail_at,primary_released_ts,year_source,year_checked_at)
VALUES(1000000000900001,'movie',900001,'Ether Cluster Fixture','Ether Cluster Fixture','ether cluster fixture',2020,1577836800,180,'en','PG',2,1,{now},{now},1,{now},1577836800,'primary',{now})
ON CONFLICT(key) DO UPDATE SET detail=1,detail_at={now},fetched_at={now},primary_released_ts=1577836800,year_source='primary',year_checked_at={now};
INSERT INTO auth_delegations(token_hash,user_id,expires_at)
SELECT '{hashlib.sha256(token.encode()).hexdigest()}',id,{now+900} FROM auth_users
WHERE username='ether035-lab-owner' AND active=1 AND origin='local';
COMMIT;
'''
    result=subprocess.run(k+['exec','-i','ether035-ha-pg-0','--','psql','-U','ether','-d','ether',
        '-v','ON_ERROR_STOP=1','-q'],input=sql,text=True,capture_output=True)
    if result.returncode: raise SystemExit('isolated lab data seed failed (details withheld)')
    print('generated title, fixture sources and short-lived local playback delegation seeded; credentials saved privately')


if __name__=='__main__': main()
