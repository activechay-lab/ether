The 40 KB `watch.ts.fixture` is a generated four-second test pattern with silent
AAC audio, H.264 video and a keyframe at its start. It contains no supplied media.
Regenerate from the repository root with:

```sh
ffmpeg -v error -y -f lavfi -i 'testsrc2=size=160x90:rate=15' \
  -f lavfi -i 'anullsrc=r=48000:cl=stereo' -t 4 -c:v libx264 -threads 1 \
  -preset veryslow -crf 35 -g 60 -pix_fmt yuv420p -c:a aac -b:a 32k \
  -f mpegts web/src/mock/watch.ts.fixture
```

The mock Vite plugin serves a sliding live window. Explicit discontinuities
separate repetitions of the fixture's original timestamps. The route is only
installed in mock mode; production uses the owner API.
