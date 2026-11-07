# AVFoundation probe

On macOS, build on demand and run outside a sandbox:

```sh
swiftc tools/avprobe/main.swift -o /tmp/ether-avprobe
/tmp/ether-avprobe 'http://127.0.0.1:1234/index.m3u8' 45
```

The JSON reports sampled video buffers per wall-clock second, playhead, stall notifications, item errors and CoreMedia error-log entries. Zero-frame seconds after the first sampled frame are an upper-bound proxy for visible freezes, not proof of slate or video content. The first-frame time has one-second resolution. Audio silence is explicitly unavailable; no audio or recovery verdict is inferred. The probe does not itself establish a pass or fail.
