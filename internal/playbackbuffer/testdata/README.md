`lab-unbounded.ts.gz` is one synthetic H.264/AAC segment recorded from Ether's
ordinary Plex HLS route in the Kubernetes generated-media lab. It contains
96 H.264 PES packets with PES_packet_length=0, matching FFmpeg output. The
contiguous publication invariant uses it without FFmpeg or network access.
It contains only generated test video/audio, with no provider media or credentials.
Gzip uses mtime=0. The decompressed fixture is 586748 bytes; SHA256:
96e8e24fb8aa3039ee72e41110bdfdbd94025c0c79190cff61f4bbc08e5e9ac9
