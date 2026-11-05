import { readFileSync } from "node:fs";
import type { Plugin } from "vite";

// hls.js uses XMLHttpRequest, so fetch-only browser mocks cannot serve media.
// Keep this route in the mock Vite server, outside the production bundle.
// Move the fixture onto each segment's live MPEG-TS clock. Repeating its
// original timestamps would create gaps when the sliding window advances.
function timedSegment(source: Buffer, sequence: number): Buffer {
  const data = Buffer.from(source);
  const wrap = 2 ** 33;
  const delta = (sequence * 4 * 90000) % wrap;
  const payloadCounts = new Map<number, number>();
  for (let packet = 0; packet + 188 <= data.length; packet += 188) {
    if (data[packet + 3]! & 0x10) {
      const pid = ((data[packet + 1]! & 31) << 8) | data[packet + 2]!;
      payloadCounts.set(pid, (payloadCounts.get(pid) ?? 0) + 1);
    }
  }
  const stamp = (at: number) => {
    const original =
      ((data[at]! >> 1) & 7) * 2 ** 30 +
      data[at + 1]! * 2 ** 22 +
      (data[at + 2]! >> 1) * 2 ** 15 +
      data[at + 3]! * 128 +
      (data[at + 4]! >> 1);
    const value = (original + delta) % wrap;
    data[at] = (data[at]! & 0xf1) | ((Math.floor(value / 2 ** 30) & 7) << 1);
    data[at + 1] = Math.floor(value / 2 ** 22) & 255;
    data[at + 2] = ((Math.floor(value / 2 ** 15) & 127) << 1) | 1;
    data[at + 3] = Math.floor(value / 128) & 255;
    data[at + 4] = ((value & 127) << 1) | 1;
  };
  for (let packet = 0; packet + 188 <= data.length; packet += 188) {
    const control = (data[packet + 3]! >> 4) & 3;
    const pid = ((data[packet + 1]! & 31) << 8) | data[packet + 2]!;
    const counter = (data[packet + 3]! + sequence * (payloadCounts.get(pid) ?? 0)) & 15;
    data[packet + 3] = (data[packet + 3]! & 0xf0) | counter;
    let payload = packet + 4;
    if (control & 2) {
      const length = data[payload]!;
      if (length >= 7 && data[payload + 1]! & 0x10) {
        const at = payload + 2;
        const base =
          data[at]! * 2 ** 25 +
          data[at + 1]! * 2 ** 17 +
          data[at + 2]! * 512 +
          data[at + 3]! * 2 +
          (data[at + 4]! >> 7);
        const value = (base + delta) % wrap;
        data[at] = Math.floor(value / 2 ** 25) & 255;
        data[at + 1] = Math.floor(value / 2 ** 17) & 255;
        data[at + 2] = Math.floor(value / 512) & 255;
        data[at + 3] = Math.floor(value / 2) & 255;
        data[at + 4] = ((value & 1) << 7) | (data[at + 4]! & 127);
      }
      payload += 1 + length;
    }
    if (!(control & 1) || !(data[packet + 1]! & 0x40) || payload + 19 > packet + 188) continue;
    if (data[payload] !== 0 || data[payload + 1] !== 0 || data[payload + 2] !== 1) continue;
    const flags = data[payload + 7]! >> 6;
    if (flags & 2) stamp(payload + 9);
    if (flags === 3) stamp(payload + 14);
  }
  return data;
}

export function mockWatch(): Plugin {
  const segment = readFileSync(new URL("./watch.ts.fixture", import.meta.url));
  return {
    name: "ether-mock-watch",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const path = (req.url ?? "").split("?")[0]!;
        if (!/^\/ether\/api\/livetv\/sessions\/mock-session-[\w-]+\//.test(path)) {
          next();
          return;
        }
        res.setHeader("Cache-Control", "no-store");
        if (path.endsWith("/index.m3u8")) {
          const end = Math.floor(Date.now() / 4000);
          const first = end - 5;
          res.setHeader("Content-Type", "application/vnd.apple.mpegurl");
          res.end(
            `#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:${first}\n` +
              Array.from({ length: 6 }, (_, i) => {
                const seq = first + i;
                return `#EXT-X-PROGRAM-DATE-TIME:${new Date(seq * 4000).toISOString()}\n#EXTINF:4.000,\n${seq}.ts\n`;
              }).join(""),
          );
        } else if (/\/\d+\.ts$/.test(path)) {
          res.setHeader("Content-Type", "video/mp2t");
          res.end(timedSegment(segment, Number(path.match(/\/(\d+)\.ts$/)![1])));
        } else {
          res.statusCode = 404;
          res.end();
        }
      });
    },
  };
}
