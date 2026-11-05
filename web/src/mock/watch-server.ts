import { readFileSync } from "node:fs";
import type { Plugin } from "vite";

// hls.js uses XMLHttpRequest, so fetch-only browser mocks cannot serve media.
// Keep this route in the mock Vite server, outside the production bundle.
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
            `#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:${first}\n#EXT-X-DISCONTINUITY-SEQUENCE:${first}\n` +
              Array.from({ length: 6 }, (_, i) => {
                const seq = first + i;
                return `${i ? "#EXT-X-DISCONTINUITY\n" : ""}#EXT-X-PROGRAM-DATE-TIME:${new Date(seq * 4000).toISOString()}\n#EXTINF:4.000,\n${seq}.ts\n`;
              }).join(""),
          );
        } else if (/\/\d+\.ts$/.test(path)) {
          res.setHeader("Content-Type", "video/mp2t");
          res.end(segment);
        } else {
          res.statusCode = 404;
          res.end();
        }
      });
    },
  };
}
