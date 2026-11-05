#!/usr/bin/env python3
"""Move completed Go masters aside, then capture real continuous session output.
Run after building cmd/onair-samples; never transcodes a clip with raw ffmpeg.
"""
import argparse, json, pathlib, subprocess, time
p=argparse.ArgumentParser();p.add_argument('--root',required=True);p.add_argument('--runner',default='/tmp/onair-samples-stream');p.add_argument('--families',default='late-night,clean,kids,cinema,retro,personal,overlay');args=p.parse_args()
root=pathlib.Path(args.root);masters=root/'masters';masters.mkdir(exist_ok=True)
for family in args.families.split(','):
 for moment in ['overlay'] if family=='overlay' else ['ident','upnext','break']:
  for width in [1920,1280]:
   name=('late-night-overlay' if family=='overlay' else family+'-'+moment)+('-720p' if width==1280 else '')
   src=masters/(name+'.mp4');dest=root/(name+'.mp4');meta=dest.with_suffix('.json')
   deadline=time.monotonic()+1800
   while not src.exists():
    if meta.exists():
     data=json.loads(meta.read_text())
     if not data.get('pipeline') and dest.exists():dest.rename(src);meta.rename(src.with_suffix('.json'));break
    if time.monotonic()>deadline:raise RuntimeError('Master not ready: '+name)
    time.sleep(2)
   if meta.exists() and dest.exists() and json.loads(meta.read_text()).get('pipeline') and json.loads(meta.read_text())['peak_1s_kbps'] <= (5000 if width==1920 else 2000):
    print('ALREADY',name,flush=True);continue
   limit=5000 if width==1920 else 2000;budget=3000 if width==1920 else 1100
   for attempt in range(4):
    print('STREAM',name,'session',budget,'kbps',flush=True)
    subprocess.run(['nice','-n','10',args.runner,'-batch','stream','-input',str(src),'-out',str(dest),'-width',str(width),'-seconds','16','-bitrate',str(budget)],check=True)
    data=json.loads(meta.read_text());peak=data['peak_1s_kbps'];avg=data['bitrate_kbps']
    print('MEASURED',name,'average',avg,'peak1s',peak,flush=True)
    if peak<=limit and avg<=limit:break
    budget=int(budget*limit/max(peak,avg)*.9)
   else:raise RuntimeError('Rate gate failed: '+name)
 print('BATCH READY',family,flush=True)
