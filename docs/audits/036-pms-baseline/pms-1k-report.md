# Baseline: PMS 1k

- container ether-036-pms-1k, image plexinc/pms-docker:1.43.5.11079-b3b13e506
- host load before: 10.27 10.14 9.46 (1/5/15 min, 16 CPUs)
- identity: version=1.43.5.11079-b3b13e506 claimed=0 machineIdentifier=272dba5f…
- first start: ready 3.065s, idle 3.012s
- idle CPU: 139.4 ms/s over 5 s
- cold starts: 40; ready after p50 3.671s (max 11.709s), then idle after p50 3.012s (max 10.262s)
- warm: 20 sequential rounds in 31.156s
- memory after warm rounds: 181 MB (peak since last start 187 MB)
- host load after: 8.38 19.92 15.86 (1/5/15 min, 16 CPUs)

Cold is the first request after a restart with the server's files evicted from the page cache. CPU is the server's whole cgroup (user + system) during the request; read and write are block-device bytes (page-cache hits are free; writes are charged at writeback). Bytes are the decompressed body; gzip is that body gzipped at the default level; wire is what the server sent to a client asking for gzip.

| client | step | status | items | cold | cold CPU | cold read | warm p50 | warm p95 | warm CPU p50 | bytes | gzip | wire | queries |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| plex-appletv | /identity | 200 | 0 | 1.7ms | 1.7ms | 0 | 0.6ms | 0.7ms | 1.0ms | 164 B | 155 B | 153 B | – |
| plex-appletv | / | 200 | 25 | 2.7ms | 2.5ms | 16.0 KB | 0.9ms | 1.2ms | 1.6ms | 2.4 KB | 932 B | 925 B | – |
| plex-appletv | /library/sections | 200 | 2 | 4.8ms | 4.0ms | 188.0 KB | 0.7ms | 1.0ms | 1.0ms | 947 B | 429 B | 421 B | – |
| plex-appletv | /hubs/promoted | 200 | 4 | 113ms | 110ms | 852.0 KB | 8.7ms | 9.5ms | 9.9ms | 25.0 KB | 3.3 KB | 3.2 KB | – |
| plex-appletv | /hubs/promoted movies directory | 200 | 3 | 14ms | 11ms | 568.0 KB | 2.1ms | 2.9ms | 2.5ms | 15.3 KB | 2.1 KB | 2.0 KB | – |
| plex-appletv | /hubs/promoted shows directory | 200 | 3 | 114ms | 109ms | 824.0 KB | 9.9ms | 13ms | 10ms | 10.1 KB | 1.6 KB | 1.6 KB | – |
| plex-appletv | /hubs/continueWatching | 200 | 1 | 21ms | 18ms | 240.0 KB | 0.6ms | 0.8ms | 0.8ms | 269 B | 196 B | 193 B | – |
| plex-appletv | /hubs | 200 | 8 | 310ms | 132ms | 600.0 KB | 14ms | 15ms | 15ms | 25.7 KB | 3.3 KB | 3.3 KB | – |
| plex-appletv | /hubs/promoted bare | 200 | 5 | 115ms | 111ms | 588.0 KB | 13ms | 16ms | 15ms | 26.1 KB | 3.6 KB | 3.5 KB | – |
| plex-appletv | /library/onDeck | 200 | 0 | 15ms | 13ms | 352.0 KB | 9.9ms | 15ms | 10ms | 186 B | 168 B | 165 B | – |
| plex-appletv | /hubs/sections/movies | 200 | 7 | 23ms | 20ms | 572.0 KB | 2.4ms | 2.9ms | 3.2ms | 16.4 KB | 2.3 KB | 2.3 KB | – |
| plex-appletv | /hubs/sections/shows | 200 | 7 | 133ms | 128ms | 608.0 KB | 15ms | 17ms | 16ms | 37.7 KB | 4.2 KB | 4.1 KB | – |
| plex-appletv | movies all titleSort p1 | 200 | 60/700 | 40ms | 32ms | 824.0 KB | 18ms | 25ms | 19ms | 76.1 KB | 7.2 KB | 7.2 KB | – |
| plex-appletv | movies all titleSort deep | 200 | 0/700 | 18ms | 12ms | 752.0 KB | 3.0ms | 3.6ms | 3.2ms | 8.6 KB | 1.6 KB | 1.6 KB | – |
| plex-appletv | movies all addedAt | 200 | 60/700 | 24ms | 20ms | 824.0 KB | 21ms | 23ms | 21ms | 76.5 KB | 8.0 KB | 7.7 KB | – |
| plex-appletv | movies all rating | 200 | 60/700 | 30ms | 25ms | 824.0 KB | 22ms | 23ms | 22ms | 76.1 KB | 7.3 KB | 7.2 KB | – |
| plex-appletv | movies genre filter list | 200 | 18 | 20ms | 16ms | 512.0 KB | 5.6ms | 6.4ms | 5.7ms | 1.9 KB | 525 B | 519 B | – |
| plex-appletv | movies jump bar | 200 | 17 | 18ms | 13ms | 636.0 KB | 4.4ms | 5.1ms | 4.7ms | 939 B | 404 B | 395 B | – |
| plex-appletv | shows all titleSort p1 | 200 | 60/300 | 54ms | 48ms | 848.0 KB | 14ms | 19ms | 15ms | 49.4 KB | 5.6 KB | 5.6 KB | – |
| plex-appletv | shows allLeaves p1 | 200 | 100/8686 | 73ms | 70ms | 788.0 KB | 70ms | 79ms | 71ms | 140.1 KB | 8.0 KB | 7.4 KB | – |
| plex-appletv | shows allLeaves deep | 200 | 100/8686 | 82ms | 79ms | 788.0 KB | 31ms | 36ms | 32ms | 142.9 KB | 7.0 KB | 7.2 KB | – |
| plex-appletv | shows all type=4 p1 | 200 | 100/8686 | 76ms | 69ms | 788.0 KB | 74ms | 85ms | 75ms | 140.2 KB | 8.1 KB | 7.4 KB | – |
| plex-appletv | shows all type=3 p1 | 200 | 100/915 | 71ms | 64ms | 776.0 KB | 32ms | 40ms | 32ms | 63.6 KB | 4.2 KB | 4.5 KB | – |
| plex-appletv | shows allLeaves updatedAt | 200 | 100/8686 | 69ms | 65ms | 788.0 KB | 56ms | 67ms | 57ms | 146.3 KB | 7.1 KB | 7.6 KB | – |
| plex-appletv | shows all type=3 updatedAt | 200 | 100/915 | 53ms | 48ms | 740.0 KB | 32ms | 35ms | 32ms | 63.6 KB | 4.2 KB | 4.5 KB | – |
| plex-appletv | movie detail | 200 | 1 | 27ms | 22ms | 672.0 KB | 11ms | 16ms | 12ms | 14.9 KB | 2.1 KB | 2.1 KB | – |
| plex-appletv | show detail | 200 | 1 | 26ms | 21ms | 676.0 KB | 9.4ms | 13ms | 10ms | 3.3 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | show children | 200 | 8 | 21ms | 16ms | 668.0 KB | 5.5ms | 6.6ms | 5.9ms | 5.7 KB | 729 B | 735 B | – |
| plex-appletv | season children | 200 | 6 | 18ms | 13ms | 672.0 KB | 4.5ms | 8.2ms | 5.1ms | 9.3 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | episode detail | 200 | 1 | 17ms | 10ms | 640.0 KB | 3.1ms | 4.9ms | 3.3ms | 2.5 KB | 994 B | 989 B | – |
| plex-appletv | movie postplay | 200 | 1 | 26ms | 24ms | 484.0 KB | 10ms | 17ms | 11ms | 14.8 KB | 2.0 KB | 2.0 KB | – |
| plex-appletv | episode postplay | 200 | 0 | 22ms | 20ms | 352.0 KB | 14ms | 17ms | 15ms | 90 B | 115 B | 102 B | – |
| plex-appletv | /hubs/search star | 200 | 17 | 1489ms | 924ms | 19.4 MB | 74ms | 82ms | 76ms | 61.4 KB | 6.1 KB | 6.0 KB | – |
| plex-appletv | /library/search star | 200 | 30 | 311ms | 167ms | 608.0 KB | 53ms | 67ms | 56ms | 37.2 KB | 3.7 KB | 3.7 KB | – |
| plex-appletv | /hubs/search the | 200 | 17 | 150ms | 137ms | 476.0 KB | 74ms | 82ms | 76ms | 66.7 KB | 6.4 KB | 6.3 KB | – |
| plex-appletv | /playlists | 200 | 0 | 8.4ms | 4.1ms | 260.0 KB | 1.7ms | 1.9ms | 1.8ms | 29 B | 54 B | 49 B | – |
| plex-appletv | /media/providers | 200 | 1 | 6.9ms | 3.4ms | 372.0 KB | 1.2ms | 1.8ms | 1.9ms | 3.2 KB | 1.2 KB | 1.2 KB | – |
| plex-appletv | /livetv/dvrs | 403 | 0 | 2.1ms | 2.1ms | 12.0 KB | 0.5ms | 0.6ms | 0.6ms | 85 B | 110 B | 78 B | – |
| plex-appletv | live guide grid 3h | 404 | 0 | 1.6ms | 1.3ms | 104.0 KB | 0.6ms | 0.7ms | 1.1ms | 85 B | 110 B | 78 B | – |
| plex-appletv | live hubs | 404 | 0 | 2.0ms | 1.5ms | 104.0 KB | 0.6ms | 0.7ms | 1.1ms | 85 B | 110 B | 78 B | – |
| plex-web | /identity | 200 | 0 | – | – | 0 | 0.5ms | 0.6ms | 1.1ms | 164 B | 155 B | 153 B | – |
| plex-web | / | 200 | 25 | – | – | 0 | 0.9ms | 1.2ms | 1.7ms | 2.4 KB | 932 B | 925 B | – |
| plex-web | /library/sections | 200 | 2 | – | – | 0 | 0.8ms | 0.9ms | 1.0ms | 947 B | 429 B | 421 B | – |
| plex-web | /hubs/promoted | 200 | 4 | – | – | 0 | 8.4ms | 9.4ms | 9.9ms | 25.0 KB | 3.3 KB | 3.2 KB | – |
| plex-web | /hubs/promoted movies directory | 200 | 3 | – | – | 0 | 1.8ms | 2.8ms | 2.4ms | 15.3 KB | 2.1 KB | 2.0 KB | – |
| plex-web | /hubs/promoted shows directory | 200 | 3 | – | – | 0 | 8.9ms | 13ms | 9.8ms | 10.1 KB | 1.6 KB | 1.6 KB | – |
| plex-web | /hubs/continueWatching | 200 | 1 | – | – | 0 | 0.6ms | 0.7ms | 0.9ms | 269 B | 196 B | 193 B | – |
| plex-web | /hubs | 200 | 8 | – | – | 0 | 13ms | 15ms | 14ms | 25.7 KB | 3.3 KB | 3.3 KB | – |
| plex-web | /hubs/promoted bare | 200 | 5 | – | – | 0 | 13ms | 15ms | 14ms | 26.1 KB | 3.6 KB | 3.5 KB | – |
| plex-web | /library/onDeck | 200 | 0 | – | – | 0 | 10ms | 17ms | 10ms | 186 B | 168 B | 165 B | – |
| plex-web | /hubs/sections/movies | 200 | 7 | – | – | 0 | 2.7ms | 3.0ms | 3.3ms | 16.4 KB | 2.3 KB | 2.3 KB | – |
| plex-web | /hubs/sections/shows | 200 | 7 | – | – | 0 | 15ms | 18ms | 17ms | 37.7 KB | 4.2 KB | 4.1 KB | – |
| plex-web | movies all titleSort p1 | 200 | 60/700 | – | – | 0 | 20ms | 26ms | 22ms | 76.1 KB | 7.2 KB | 7.2 KB | – |
| plex-web | movies all titleSort deep | 200 | 0/700 | – | – | 0 | 3.1ms | 3.4ms | 3.3ms | 8.6 KB | 1.6 KB | 1.6 KB | – |
| plex-web | movies all addedAt | 200 | 60/700 | – | – | 0 | 16ms | 23ms | 17ms | 76.5 KB | 8.0 KB | 7.7 KB | – |
| plex-web | movies all rating | 200 | 60/700 | – | – | 0 | 14ms | 26ms | 14ms | 76.1 KB | 7.3 KB | 7.2 KB | – |
| plex-web | movies genre filter list | 200 | 18 | – | – | 0 | 5.5ms | 6.3ms | 5.6ms | 1.9 KB | 525 B | 519 B | – |
| plex-web | movies jump bar | 200 | 17 | – | – | 0 | 4.2ms | 4.8ms | 4.5ms | 939 B | 404 B | 395 B | – |
| plex-web | shows all titleSort p1 | 200 | 60/300 | – | – | 0 | 12ms | 17ms | 13ms | 49.4 KB | 5.6 KB | 5.6 KB | – |
| plex-web | shows allLeaves p1 | 200 | 100/8686 | – | – | 0 | 72ms | 78ms | 74ms | 140.1 KB | 8.0 KB | 7.4 KB | – |
| plex-web | shows allLeaves deep | 200 | 100/8686 | – | – | 0 | 32ms | 37ms | 33ms | 142.9 KB | 7.0 KB | 7.2 KB | – |
| plex-web | shows all type=4 p1 | 200 | 100/8686 | – | – | 0 | 61ms | 80ms | 61ms | 140.2 KB | 8.1 KB | 7.4 KB | – |
| plex-web | shows all type=3 p1 | 200 | 100/915 | – | – | 0 | 31ms | 34ms | 32ms | 63.6 KB | 4.2 KB | 4.5 KB | – |
| plex-web | shows allLeaves updatedAt | 200 | 100/8686 | – | – | 0 | 63ms | 68ms | 64ms | 146.3 KB | 7.1 KB | 7.6 KB | – |
| plex-web | shows all type=3 updatedAt | 200 | 100/915 | – | – | 0 | 32ms | 33ms | 33ms | 63.6 KB | 4.2 KB | 4.5 KB | – |
| plex-web | movie detail | 200 | 1 | – | – | 0 | 12ms | 13ms | 12ms | 14.9 KB | 2.1 KB | 2.1 KB | – |
| plex-web | show detail | 200 | 1 | – | – | 0 | 9.0ms | 10ms | 9.3ms | 3.3 KB | 1.1 KB | 1.1 KB | – |
| plex-web | show children | 200 | 8 | – | – | 0 | 5.6ms | 7.0ms | 5.9ms | 5.7 KB | 729 B | 735 B | – |
| plex-web | season children | 200 | 6 | – | – | 0 | 4.4ms | 7.7ms | 5.0ms | 9.3 KB | 1.1 KB | 1.1 KB | – |
| plex-web | episode detail | 200 | 1 | – | – | 0 | 2.8ms | 4.2ms | 3.1ms | 2.5 KB | 994 B | 989 B | – |
| plex-web | movie postplay | 200 | 1 | – | – | 0 | 9.5ms | 10ms | 10ms | 14.8 KB | 2.0 KB | 2.0 KB | – |
| plex-web | episode postplay | 200 | 0 | – | – | 0 | 11ms | 16ms | 11ms | 90 B | 115 B | 102 B | – |
| plex-web | /hubs/search star | 200 | 17 | – | – | 0 | 71ms | 77ms | 72ms | 61.4 KB | 6.1 KB | 6.0 KB | – |
| plex-web | /library/search star | 200 | 30 | – | – | 0 | 65ms | 69ms | 65ms | 37.2 KB | 3.7 KB | 3.7 KB | – |
| plex-web | /hubs/search the | 200 | 17 | – | – | 0 | 75ms | 81ms | 78ms | 66.7 KB | 6.4 KB | 6.3 KB | – |
| plex-web | /playlists | 200 | 0 | – | – | 0 | 1.6ms | 2.2ms | 1.7ms | 29 B | 54 B | 49 B | – |
| plex-web | /media/providers | 200 | 1 | – | – | 0 | 1.4ms | 1.8ms | 1.9ms | 3.2 KB | 1.2 KB | 1.2 KB | – |
| plex-web | /livetv/dvrs | 403 | 0 | – | – | 0 | 0.5ms | 0.6ms | 0.6ms | 85 B | 110 B | 78 B | – |
| plex-web | live guide grid 3h | 404 | 0 | – | – | 0 | 0.6ms | 0.7ms | 1.1ms | 85 B | 110 B | 78 B | – |
| plex-web | live hubs | 404 | 0 | – | – | 0 | 0.6ms | 0.7ms | 1.1ms | 85 B | 110 B | 78 B | – |
