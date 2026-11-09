# Baseline: Ether 54k

- container ether-036-pms-ether-54k, image debian:trixie-slim
- host load before: 7.53 9.88 13.54 (1/5/15 min, 16 CPUs)
- identity: version=1.43.4.10903-e5521bd8c claimed=0 machineIdentifier=2eae892b…
- first start: ready 28.147s, idle 6.024s
- idle CPU: 4.3 ms/s over 5 s
- cold starts: 40; ready after p50 3.517s (max 41.124s), then idle after p50 4.017s (max 6.025s)
- warm: 20 sequential rounds in 51.459s
- memory after warm rounds: 612 MB (peak since last start 616 MB)
- host load after: 2.73 3.98 7.27 (1/5/15 min, 16 CPUs)

Cold is the first request after a restart with the server's files evicted from the page cache. CPU is the server's whole cgroup (user + system) during the request; read and write are block-device bytes (page-cache hits are free; writes are charged at writeback). Bytes are the decompressed body; gzip is that body gzipped at the default level; wire is what the server sent to a client asking for gzip.

| client | step | status | items | cold | cold CPU | cold read | warm p50 | warm p95 | warm CPU p50 | bytes | gzip | wire | queries |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| plex-appletv | /identity | 200 | 0 | 2.6ms | 2.6ms | 0 | 1.6ms | 2.0ms | 1.5ms | 165 B | 156 B | 165 B | – |
| plex-appletv | / | 200 | 23 | 3.3ms | 4.0ms | 0 | 2.3ms | 2.8ms | 3.3ms | 2.4 KB | 891 B | 891 B | – |
| plex-appletv | /library/sections | 200 | 2 | 3.2ms | 3.6ms | 0 | 1.8ms | 2.1ms | 2.6ms | 898 B | 389 B | 898 B | – |
| plex-appletv | /hubs/promoted | 200 | 10 | 182ms | 200ms | 16.0 KB | 3.5ms | 4.0ms | 4.8ms | 171.5 KB | 18.8 KB | 18.8 KB | – |
| plex-appletv | /hubs/promoted movies directory | 200 | 17 | 264ms | 326ms | 16.0 KB | 3.4ms | 4.1ms | 4.7ms | 293.8 KB | 31.1 KB | 31.1 KB | – |
| plex-appletv | /hubs/promoted shows directory | 200 | 3 | 110ms | 195ms | 16.0 KB | 3.4ms | 4.0ms | 4.9ms | 36.5 KB | 4.4 KB | 4.4 KB | – |
| plex-appletv | /hubs/continueWatching | 200 | 1 | 326ms | 32ms | 16.0 KB | 19ms | 26ms | 22ms | 26.2 KB | 3.5 KB | 3.5 KB | – |
| plex-appletv | /hubs | 200 | 10 | 394ms | 167ms | 16.0 KB | 3.5ms | 3.9ms | 4.5ms | 171.5 KB | 18.8 KB | 18.8 KB | – |
| plex-appletv | /hubs/promoted bare | 200 | 10 | 653ms | 239ms | 16.0 KB | 3.4ms | 4.0ms | 4.7ms | 171.5 KB | 18.8 KB | 18.8 KB | – |
| plex-appletv | /library/onDeck | 200 | 500/941 | 1920ms | 970ms | 16.0 KB | 562ms | 614ms | 684ms | 1266.1 KB | 92.4 KB | 92.4 KB | – |
| plex-appletv | /hubs/sections/movies | 200 | 10 | 449ms | 156ms | 16.0 KB | 4.3ms | 10ms | 7.2ms | 160.7 KB | 17.5 KB | 17.5 KB | – |
| plex-appletv | /hubs/sections/shows | 200 | 4 | 185ms | 283ms | 16.0 KB | 3.1ms | 3.6ms | 4.3ms | 68.6 KB | 7.9 KB | 7.9 KB | – |
| plex-appletv | movies all titleSort p1 | 200 | 60/24969 | 230ms | 81ms | 16.0 KB | 23ms | 29ms | 24ms | 86.2 KB | 9.5 KB | 9.5 KB | – |
| plex-appletv | movies all titleSort deep | 200 | 60/24969 | 1101ms | 99ms | 16.0 KB | 22ms | 29ms | 25ms | 85.6 KB | 9.5 KB | 9.5 KB | – |
| plex-appletv | movies all addedAt | 200 | 60/24969 | 640ms | 81ms | 16.0 KB | 22ms | 28ms | 25ms | 85.7 KB | 9.2 KB | 9.2 KB | – |
| plex-appletv | movies all rating | 200 | 60/24969 | 39ms | 73ms | 16.0 KB | 22ms | 27ms | 24ms | 85.7 KB | 9.8 KB | 9.8 KB | – |
| plex-appletv | movies genre filter list | 200 | 19 | 116ms | 118ms | 0 | 2.4ms | 3.1ms | 3.3ms | 2.2 KB | 532 B | 532 B | – |
| plex-appletv | movies jump bar | 200 | 25 | 101ms | 105ms | 0 | 2.5ms | 3.4ms | 3.4ms | 2.4 KB | 557 B | 557 B | – |
| plex-appletv | shows all titleSort p1 | 200 | 60/1000 | 1207ms | 243ms | 16.0 KB | 90ms | 116ms | 100ms | 60.3 KB | 7.1 KB | 7.1 KB | – |
| plex-appletv | shows allLeaves p1 | 200 | 100/23937 | 57ms | 61ms | 0 | 33ms | 38ms | 35ms | 175.6 KB | 6.6 KB | 6.6 KB | – |
| plex-appletv | shows allLeaves deep | 200 | 100/23937 | 84ms | 88ms | 0 | 30ms | 35ms | 33ms | 176.4 KB | 6.8 KB | 6.8 KB | – |
| plex-appletv | shows all type=4 p1 | 200 | 100/23937 | 54ms | 58ms | 0 | 33ms | 36ms | 35ms | 175.6 KB | 6.6 KB | 6.6 KB | – |
| plex-appletv | shows all type=3 p1 | 200 | 100/3991 | 111ms | 146ms | 0 | 45ms | 55ms | 51ms | 93.8 KB | 5.5 KB | 5.5 KB | – |
| plex-appletv | shows allLeaves updatedAt | 200 | 100/23937 | 55ms | 59ms | 0 | 33ms | 38ms | 35ms | 175.6 KB | 6.6 KB | 6.6 KB | – |
| plex-appletv | shows all type=3 updatedAt | 200 | 100/3991 | 81ms | 85ms | 0 | 40ms | 55ms | 41ms | 93.8 KB | 5.5 KB | 5.5 KB | – |
| plex-appletv | movie detail | 200 | 1 | 480ms | 20ms | 16.0 KB | 7.7ms | 9.1ms | 9.2ms | 2.8 KB | 1.2 KB | 1.2 KB | – |
| plex-appletv | show detail | 200 | 1 | 48ms | 36ms | 16.0 KB | 9.6ms | 12ms | 12ms | 3.3 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | show children | 200 | 4 | 18ms | 21ms | 0 | 9.6ms | 11ms | 11ms | 4.3 KB | 761 B | 761 B | – |
| plex-appletv | season children | 200 | 6 | 258ms | 46ms | 0 | 12ms | 14ms | 15ms | 11.0 KB | 1.2 KB | 1.2 KB | – |
| plex-appletv | episode detail | 200 | 1 | 257ms | 18ms | 0 | 7.5ms | 9.1ms | 9.0ms | 2.9 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | movie postplay | 200 | 0 | 4.1ms | 5.3ms | 0 | 4.0ms | 4.7ms | 5.3ms | 91 B | 116 B | 91 B | – |
| plex-appletv | episode postplay | 200 | 1 | 26ms | 61ms | 0 | 9.5ms | 11ms | 11ms | 2.8 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | /hubs/search star | 200 | 17 | 80ms | 55ms | 16.0 KB | 22ms | 29ms | 28ms | 30.8 KB | 3.8 KB | 3.8 KB | – |
| plex-appletv | /library/search star | 200 | 20 | 24ms | 47ms | 16.0 KB | 21ms | 26ms | 24ms | 27.9 KB | 3.4 KB | 3.4 KB | – |
| plex-appletv | /hubs/search the | 200 | 17 | 4.1ms | 5.8ms | 0 | 4.5ms | 5.1ms | 6.6ms | 1.9 KB | 360 B | 360 B | – |
| plex-appletv | /playlists | 200 | 0 | 3.6ms | 4.4ms | 0 | 2.8ms | 3.2ms | 3.7ms | 30 B | 55 B | 30 B | – |
| plex-appletv | /media/providers | 200 | 1 | 3.3ms | 4.0ms | 0 | 2.8ms | 3.2ms | 3.4ms | 4.4 KB | 1.3 KB | 1.3 KB | – |
| plex-appletv | /livetv/dvrs | 404 | 0 | 2.3ms | 2.8ms | 0 | 1.7ms | 1.9ms | 2.4ms | 85 B | 110 B | 85 B | – |
| plex-appletv | live guide grid 3h | 404 | 0 | 2.4ms | 3.0ms | 0 | 1.6ms | 1.9ms | 2.3ms | 85 B | 110 B | 85 B | – |
| plex-appletv | live hubs | 404 | 0 | 2.2ms | 2.6ms | 0 | 1.6ms | 2.1ms | 2.3ms | 85 B | 110 B | 85 B | – |
| plex-web | /identity | 200 | 0 | – | – | 0 | 1.6ms | 1.9ms | 1.6ms | 165 B | 156 B | 165 B | – |
| plex-web | / | 200 | 23 | – | – | 0 | 2.6ms | 3.4ms | 3.4ms | 2.4 KB | 891 B | 891 B | – |
| plex-web | /library/sections | 200 | 2 | – | – | 0 | 1.7ms | 1.9ms | 2.5ms | 898 B | 389 B | 898 B | – |
| plex-web | /hubs/promoted | 200 | 10 | – | – | 0 | 3.4ms | 4.1ms | 4.7ms | 173.9 KB | 18.9 KB | 18.9 KB | – |
| plex-web | /hubs/promoted movies directory | 200 | 16 | – | – | 0 | 3.4ms | 3.9ms | 4.8ms | 280.8 KB | 29.7 KB | 29.7 KB | – |
| plex-web | /hubs/promoted shows directory | 200 | 3 | – | – | 0 | 3.2ms | 3.5ms | 4.5ms | 36.5 KB | 4.3 KB | 4.3 KB | – |
| plex-web | /hubs/continueWatching | 200 | 1 | – | – | 0 | 20ms | 23ms | 23ms | 26.0 KB | 3.5 KB | 3.5 KB | – |
| plex-web | /hubs | 200 | 10 | – | – | 0 | 3.4ms | 4.2ms | 4.6ms | 173.9 KB | 18.9 KB | 18.9 KB | – |
| plex-web | /hubs/promoted bare | 200 | 10 | – | – | 0 | 3.4ms | 3.8ms | 4.6ms | 173.9 KB | 18.9 KB | 18.9 KB | – |
| plex-web | /library/onDeck | 200 | 500/941 | – | – | 0 | 585ms | 632ms | 711ms | 1235.8 KB | 92.0 KB | 92.0 KB | – |
| plex-web | /hubs/sections/movies | 200 | 9 | – | – | 0 | 3.8ms | 10ms | 5.4ms | 145.9 KB | 15.9 KB | 15.9 KB | – |
| plex-web | /hubs/sections/shows | 200 | 4 | – | – | 0 | 3.1ms | 3.8ms | 4.0ms | 67.9 KB | 7.8 KB | 7.8 KB | – |
| plex-web | movies all titleSort p1 | 200 | 60/24969 | – | – | 0 | 24ms | 25ms | 26ms | 88.2 KB | 9.6 KB | 9.6 KB | – |
| plex-web | movies all titleSort deep | 200 | 60/24969 | – | – | 0 | 24ms | 28ms | 26ms | 87.5 KB | 9.6 KB | 9.6 KB | – |
| plex-web | movies all addedAt | 200 | 60/24969 | – | – | 0 | 24ms | 28ms | 26ms | 87.7 KB | 9.3 KB | 9.3 KB | – |
| plex-web | movies all rating | 200 | 60/24969 | – | – | 0 | 23ms | 30ms | 25ms | 87.7 KB | 9.9 KB | 9.9 KB | – |
| plex-web | movies genre filter list | 200 | 19 | – | – | 0 | 2.3ms | 2.6ms | 2.8ms | 2.2 KB | 532 B | 532 B | – |
| plex-web | movies jump bar | 200 | 25 | – | – | 0 | 2.4ms | 2.8ms | 3.1ms | 2.4 KB | 557 B | 557 B | – |
| plex-web | shows all titleSort p1 | 200 | 60/1000 | – | – | 0 | 93ms | 107ms | 106ms | 60.3 KB | 7.1 KB | 7.1 KB | – |
| plex-web | shows allLeaves p1 | 200 | 100/23937 | – | – | 0 | 36ms | 41ms | 39ms | 178.8 KB | 6.6 KB | 6.6 KB | – |
| plex-web | shows allLeaves deep | 200 | 100/23937 | – | – | 0 | 22ms | 38ms | 28ms | 179.6 KB | 6.8 KB | 6.8 KB | – |
| plex-web | shows all type=4 p1 | 200 | 100/23937 | – | – | 0 | 36ms | 40ms | 39ms | 178.8 KB | 6.6 KB | 6.6 KB | – |
| plex-web | shows all type=3 p1 | 200 | 100/3991 | – | – | 0 | 50ms | 53ms | 53ms | 93.8 KB | 5.5 KB | 5.5 KB | – |
| plex-web | shows allLeaves updatedAt | 200 | 100/23937 | – | – | 0 | 35ms | 41ms | 37ms | 178.8 KB | 6.6 KB | 6.6 KB | – |
| plex-web | shows all type=3 updatedAt | 200 | 100/3991 | – | – | 0 | 40ms | 52ms | 47ms | 93.8 KB | 5.5 KB | 5.5 KB | – |
| plex-web | movie detail | 200 | 1 | – | – | 0 | 7.0ms | 9.6ms | 8.4ms | 2.7 KB | 1.1 KB | 1.1 KB | – |
| plex-web | show detail | 200 | 1 | – | – | 0 | 9.9ms | 13ms | 13ms | 3.4 KB | 1.1 KB | 1.1 KB | – |
| plex-web | show children | 200 | 4 | – | – | 0 | 9.3ms | 11ms | 11ms | 4.3 KB | 761 B | 761 B | – |
| plex-web | season children | 200 | 6 | – | – | 0 | 11ms | 16ms | 14ms | 11.2 KB | 1.2 KB | 1.2 KB | – |
| plex-web | episode detail | 200 | 1 | – | – | 0 | 7.5ms | 10ms | 9.5ms | 2.8 KB | 1.1 KB | 1.1 KB | – |
| plex-web | movie postplay | 200 | 0 | – | – | 0 | 3.6ms | 4.7ms | 4.8ms | 91 B | 116 B | 91 B | – |
| plex-web | episode postplay | 200 | 1 | – | – | 0 | 9.4ms | 11ms | 11ms | 2.7 KB | 1.0 KB | 1.0 KB | – |
| plex-web | /hubs/search star | 200 | 17 | – | – | 0 | 23ms | 26ms | 29ms | 31.4 KB | 3.9 KB | 3.9 KB | – |
| plex-web | /library/search star | 200 | 20 | – | – | 0 | 21ms | 25ms | 26ms | 28.5 KB | 3.5 KB | 3.5 KB | – |
| plex-web | /hubs/search the | 200 | 17 | – | – | 0 | 4.4ms | 5.1ms | 6.3ms | 1.9 KB | 360 B | 360 B | – |
| plex-web | /playlists | 200 | 0 | – | – | 0 | 2.9ms | 3.3ms | 3.8ms | 30 B | 55 B | 30 B | – |
| plex-web | /media/providers | 200 | 1 | – | – | 0 | 2.7ms | 3.0ms | 3.6ms | 4.4 KB | 1.3 KB | 1.3 KB | – |
| plex-web | /livetv/dvrs | 404 | 0 | – | – | 0 | 1.7ms | 1.8ms | 2.4ms | 85 B | 110 B | 85 B | – |
| plex-web | live guide grid 3h | 404 | 0 | – | – | 0 | 1.7ms | 2.0ms | 2.6ms | 85 B | 110 B | 85 B | – |
| plex-web | live hubs | 404 | 0 | – | – | 0 | 1.6ms | 1.9ms | 2.2ms | 85 B | 110 B | 85 B | – |
