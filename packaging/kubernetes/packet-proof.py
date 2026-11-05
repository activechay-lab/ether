#!/usr/bin/env python3
"""Demux collected generated TS with packaged ffprobe and reject frame DTS overlap."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re
import subprocess


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--image',required=True)
    p.add_argument('--media-dir',required=True,type=Path)
    p.add_argument('--output',required=True,type=Path)
    a=p.parse_args()
    if not re.fullmatch(r'ether035-ha:[A-Za-z0-9_.-]+',a.image): p.error('only a named lab image is allowed')
    segments=sorted(a.media_dir.glob('segment-*.ts'))
    if not segments: p.error('no collected MPEG-TS segments')
    a.output.parent.mkdir(parents=True,exist_ok=True)
    combined=a.media_dir/'packet-proof-input.ts'
    with combined.open('wb') as out:
        for segment in segments:
            with segment.open('rb') as data:
                while chunk:=data.read(1024*1024): out.write(chunk)
    # The container is exact-name owned, --rm, no network, bounded resources;
    # stdin contains only generated media, never authentication headers/config.
    created=subprocess.run(['docker','create','--interactive','--name','ether035-ha-packet-proof',
        '--network','none','--memory','512m','--cpus','1','--entrypoint','/usr/lib/ether/ffprobe',a.image,
        '-v','error','-show_packets','-show_entries','packet=codec_type,pts_time,dts_time','-of','json','pipe:0'],
        capture_output=True,text=True,check=True)
    container_id=created.stdout.strip()
    if not re.fullmatch(r'[a-f0-9]{64}',container_id): raise SystemExit('invalid owned proof container ID')
    try:
        with combined.open('rb') as media:
            r=subprocess.run(['docker','start','--attach','--interactive',container_id],
                stdin=media,capture_output=True,timeout=60)
    finally:
        # Capture immutable ID before start, and clean up even on timeout.
        subprocess.run(['docker','stop','--time','0',container_id],capture_output=True,timeout=15)
        subprocess.run(['docker','rm','--volumes',container_id],capture_output=True,timeout=15,check=True)
    if r.returncode:
        a.output.write_text(json.dumps(dict(packetTimingVerified=False,reason='packaged demux failed'))+'\n')
        raise SystemExit(1)
    packets=json.loads(r.stdout)['packets']
    last,missing,counts={},Counter(),Counter()
    violations=[]
    for n,packet in enumerate(packets):
        stream=packet['codec_type']
        if stream not in ('video','audio'):continue
        counts[stream]+=1
        if 'dts_time' not in packet:
            missing[stream]+=1;continue
        dts=float(packet['dts_time'])
        if stream in last and dts<=last[stream]:
            violations.append(dict(packet=n,stream=stream,previousDTS=last[stream],dts=dts))
        last[stream]=dts
    passed=not violations and not r.stderr and counts['video']>=2 and counts['audio']>=2 and missing['audio']==0 and missing['video']<=1
    summary=dict(packetTimingVerified=passed,packets=len(packets),streamPackets=dict(counts),
                 missingDTS=dict(missing),nonIncreasingDTS=violations,demuxErrorBytes=len(r.stderr),
                 mediaDecodeVerified=False,placementAndFencingVerified=False)
    a.output.write_text(json.dumps(summary,indent=2)+'\n')
    print(json.dumps(summary))
    raise SystemExit(0 if passed else 1)


if __name__=='__main__':main()
