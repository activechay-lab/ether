The small `watch.ts.fixture` is a generated four-second test pattern with H.264 video and a keyframe at its start. It contains no supplied media.
Regenerate from the repository root with:

```sh
ffmpeg -v error -y -f lavfi -i 'testsrc2=size=160x90:rate=15' \
  -t 4 -c:v libx264 -threads 1 \
  -preset veryslow -crf 35 -g 60 -pix_fmt yuv420p -an \
  -f mpegts web/src/mock/watch.ts.fixture
```

The mock Vite plugin serves a sliding live window. The mock shifts PES timestamps and PCR onto each segment's clock so the
fixture repeats continuously without discontinuities or accumulated drift. The route is only
installed in mock mode; production uses the owner API.
