# Baseline: Ether 540k

- container ether-036-pms-ether-540k, image debian:trixie-slim
- host load before: 2.64 3.59 4.21 (1/5/15 min, 16 CPUs)
- identity: version=1.43.4.10903-e5521bd8c claimed=0 machineIdentifier=8f68e86f…
- first start: ready 15.083s, idle 1m50.395s
- idle CPU: 339.7 ms/s over 5 s
- cold starts: 40; ready after p50 19.442s (max 27.486s), then idle after p50 23.078s (max 1m58.418s)
- warm: 20 sequential rounds in 1m16.397s
- memory after warm rounds: 2492 MB (peak since last start 2807 MB)
- host load after: 3.71 4.28 4.82 (1/5/15 min, 16 CPUs)

Cold is the first request after a restart with the server's files evicted from the page cache. CPU is the server's whole cgroup (user + system) during the request; read and write are block-device bytes (page-cache hits are free; writes are charged at writeback). Bytes are the decompressed body; gzip is that body gzipped at the default level; wire is what the server sent to a client asking for gzip.

| client | step | status | items | cold | cold CPU | cold read | warm p50 | warm p95 | warm CPU p50 | bytes | gzip | wire | queries |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| plex-appletv | /identity | 200 | 0 | 2.2ms | 2.3ms | 4.0 KB | 1.6ms | 2.1ms | 2.1ms | 165 B | 157 B | 165 B | – |
| plex-appletv | / | 200 | 23 | 3.0ms | 4.1ms | 16.0 KB | 2.2ms | 2.7ms | 3.5ms | 2.4 KB | 892 B | 892 B | – |
| plex-appletv | /library/sections | 200 | 2 | 2.5ms | 2.8ms | 0 | 1.8ms | 2.1ms | 3.4ms | 898 B | 390 B | 898 B | – |
| plex-appletv | /hubs/promoted | 200 | 10 | 86ms | 49ms | 60.0 KB | 3.2ms | 4.1ms | 6.7ms | 165.6 KB | 18.3 KB | 18.3 KB | – |
| plex-appletv | /hubs/promoted movies directory | 200 | 16 | 326ms | 155ms | 292.0 KB | 3.5ms | 4.1ms | 6.3ms | 270.7 KB | 29.0 KB | 29.0 KB | – |
| plex-appletv | /hubs/promoted shows directory | 200 | 3 | 868ms | 169ms | 540.0 KB | 3.4ms | 4.5ms | 7.4ms | 36.5 KB | 4.4 KB | 4.4 KB | – |
| plex-appletv | /hubs/continueWatching | 200 | 1 | 47ms | 23ms | 28.0 KB | 16ms | 21ms | 33ms | 25.2 KB | 3.4 KB | 3.4 KB | – |
| plex-appletv | /hubs | 200 | 10 | 134ms | 76ms | 72.0 KB | 3.7ms | 4.0ms | 7.8ms | 165.6 KB | 18.3 KB | 18.3 KB | – |
| plex-appletv | /hubs/promoted bare | 200 | 10 | 158ms | 48ms | 100.0 KB | 3.5ms | 4.4ms | 7.3ms | 165.6 KB | 18.3 KB | 18.3 KB | – |
| plex-appletv | /library/onDeck | 200 | 500/6058 | 76484ms | 27281ms | 103.3 MB | 1143ms | 1367ms | 2249ms | 1267.7 KB | 93.8 KB | 93.8 KB | – |
| plex-appletv | /hubs/sections/movies | 200 | 9 | 278ms | 420ms | 308.0 KB | 4.4ms | 32ms | 9.3ms | 143.3 KB | 15.7 KB | 15.7 KB | – |
| plex-appletv | /hubs/sections/shows | 200 | 4 | 3317ms | 539ms | 2848.0 KB | 3.1ms | 4.0ms | 6.5ms | 68.7 KB | 7.9 KB | 7.9 KB | – |
| plex-appletv | movies all titleSort p1 | 200 | 60/249760 | 255ms | 98ms | 248.0 KB | 23ms | 31ms | 46ms | 85.7 KB | 9.1 KB | 9.1 KB | – |
| plex-appletv | movies all titleSort deep | 200 | 60/249760 | 1030ms | 188ms | 1068.0 KB | 22ms | 30ms | 38ms | 85.7 KB | 9.2 KB | 9.2 KB | – |
| plex-appletv | movies all addedAt | 200 | 60/249760 | 68ms | 83ms | 48.0 KB | 21ms | 30ms | 45ms | 86.0 KB | 9.2 KB | 9.2 KB | – |
| plex-appletv | movies all rating | 200 | 60/249760 | 175ms | 136ms | 124.0 KB | 23ms | 29ms | 36ms | 85.0 KB | 9.8 KB | 9.8 KB | – |
| plex-appletv | movies genre filter list | 200 | 19 | 26383ms | 4662ms | 31.6 MB | 2.3ms | 2.7ms | 5.2ms | 2.2 KB | 535 B | 535 B | – |
| plex-appletv | movies jump bar | 200 | 25 | 13727ms | 3178ms | 12.9 MB | 2.5ms | 2.8ms | 5.6ms | 2.5 KB | 574 B | 574 B | – |
| plex-appletv | shows all titleSort p1 | 200 | 60/10000 | 1850ms | 364ms | 2152.0 KB | 104ms | 133ms | 206ms | 60.3 KB | 6.9 KB | 6.9 KB | – |
| plex-appletv | shows allLeaves p1 | 200 | 100/239558 | 18989ms | 1565ms | 13.8 MB | 33ms | 43ms | 62ms | 174.7 KB | 6.6 KB | 6.6 KB | – |
| plex-appletv | shows allLeaves deep | 200 | 100/239558 | 4178ms | 428ms | 9528.0 KB | 26ms | 35ms | 48ms | 175.2 KB | 6.7 KB | 6.7 KB | – |
| plex-appletv | shows all type=4 p1 | 200 | 100/239558 | 4770ms | 455ms | 10212.0 KB | 33ms | 40ms | 51ms | 174.7 KB | 6.6 KB | 6.6 KB | – |
| plex-appletv | shows all type=3 p1 | 200 | 100/39944 | 8449ms | 1172ms | 12.8 MB | 51ms | 64ms | 96ms | 92.6 KB | 5.5 KB | 5.5 KB | – |
| plex-appletv | shows allLeaves updatedAt | 200 | 100/239558 | 13169ms | 960ms | 16.5 MB | 32ms | 39ms | 44ms | 174.7 KB | 6.6 KB | 6.6 KB | – |
| plex-appletv | shows all type=3 updatedAt | 200 | 100/39944 | 14524ms | 1543ms | 10.7 MB | 53ms | 60ms | 80ms | 92.6 KB | 5.5 KB | 5.5 KB | – |
| plex-appletv | movie detail | 200 | 1 | 278ms | 46ms | 124.0 KB | 7.8ms | 10ms | 11ms | 2.8 KB | 1.2 KB | 1.2 KB | – |
| plex-appletv | show detail | 200 | 1 | 256ms | 41ms | 136.0 KB | 10ms | 17ms | 21ms | 3.4 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | show children | 200 | 4 | 16ms | 20ms | 32.0 KB | 9.2ms | 12ms | 19ms | 4.2 KB | 758 B | 758 B | – |
| plex-appletv | season children | 200 | 6 | 257ms | 30ms | 148.0 KB | 11ms | 16ms | 23ms | 10.9 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | episode detail | 200 | 1 | 255ms | 21ms | 68.0 KB | 7.2ms | 8.5ms | 15ms | 2.9 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | movie postplay | 200 | 0 | 77ms | 8.9ms | 44.0 KB | 3.4ms | 4.5ms | 7.0ms | 91 B | 116 B | 91 B | – |
| plex-appletv | episode postplay | 200 | 1 | 115ms | 41ms | 124.0 KB | 8.6ms | 13ms | 16ms | 2.8 KB | 1.1 KB | 1.1 KB | – |
| plex-appletv | /hubs/search star | 200 | 17 | 3182ms | 338ms | 2076.0 KB | 67ms | 83ms | 106ms | 50.3 KB | 5.7 KB | 5.7 KB | – |
| plex-appletv | /library/search star | 200 | 30 | 2714ms | 249ms | 1720.0 KB | 42ms | 48ms | 79ms | 42.0 KB | 4.8 KB | 4.8 KB | – |
| plex-appletv | /hubs/search the | 200 | 17 | 664ms | 54ms | 568.0 KB | 4.7ms | 6.0ms | 11ms | 1.9 KB | 360 B | 360 B | – |
| plex-appletv | /playlists | 200 | 0 | 2.7ms | 3.0ms | 4.0 KB | 2.6ms | 3.8ms | 5.7ms | 30 B | 55 B | 30 B | – |
| plex-appletv | /media/providers | 200 | 1 | 3.6ms | 3.5ms | 0 | 2.5ms | 3.6ms | 4.1ms | 4.4 KB | 1.4 KB | 1.4 KB | – |
| plex-appletv | /livetv/dvrs | 404 | 0 | 2.1ms | 2.2ms | 0 | 1.6ms | 1.8ms | 2.8ms | 85 B | 110 B | 85 B | – |
| plex-appletv | live guide grid 3h | 404 | 0 | 2.4ms | 2.4ms | 0 | 1.7ms | 2.2ms | 3.0ms | 85 B | 110 B | 85 B | – |
| plex-appletv | live hubs | 404 | 0 | 2.5ms | 3.6ms | 0 | 1.7ms | 1.9ms | 3.4ms | 85 B | 110 B | 85 B | – |
| plex-web | /identity | 200 | 0 | – | – | 0 | 1.6ms | 2.2ms | 2.2ms | 165 B | 157 B | 165 B | – |
| plex-web | / | 200 | 23 | – | – | 0 | 2.2ms | 2.5ms | 4.8ms | 2.4 KB | 892 B | 892 B | – |
| plex-web | /library/sections | 200 | 2 | – | – | 0 | 1.7ms | 2.3ms | 3.4ms | 898 B | 390 B | 898 B | – |
| plex-web | /hubs/promoted | 200 | 10 | – | – | 0 | 3.2ms | 4.1ms | 5.8ms | 168.0 KB | 18.4 KB | 18.4 KB | – |
| plex-web | /hubs/promoted movies directory | 200 | 16 | – | – | 0 | 3.6ms | 4.6ms | 8.4ms | 275.4 KB | 29.4 KB | 29.4 KB | – |
| plex-web | /hubs/promoted shows directory | 200 | 3 | – | – | 0 | 3.6ms | 4.0ms | 6.9ms | 36.4 KB | 4.4 KB | 4.4 KB | – |
| plex-web | /hubs/continueWatching | 200 | 1 | – | – | 0 | 21ms | 25ms | 41ms | 25.0 KB | 3.4 KB | 3.4 KB | – |
| plex-web | /hubs | 200 | 10 | – | – | 0 | 3.5ms | 4.0ms | 7.6ms | 168.0 KB | 18.4 KB | 18.4 KB | – |
| plex-web | /hubs/promoted bare | 200 | 10 | – | – | 0 | 3.6ms | 4.2ms | 8.4ms | 168.0 KB | 18.4 KB | 18.4 KB | – |
| plex-web | /library/onDeck | 200 | 500/6058 | – | – | 0 | 1050ms | 1315ms | 1866ms | 1237.4 KB | 93.4 KB | 93.4 KB | – |
| plex-web | /hubs/sections/movies | 200 | 9 | – | – | 0 | 4.7ms | 35ms | 9.9ms | 146.3 KB | 16.1 KB | 16.1 KB | – |
| plex-web | /hubs/sections/shows | 200 | 4 | – | – | 0 | 3.1ms | 3.9ms | 6.3ms | 67.9 KB | 7.8 KB | 7.8 KB | – |
| plex-web | movies all titleSort p1 | 200 | 60/249760 | – | – | 0 | 24ms | 35ms | 33ms | 87.6 KB | 9.3 KB | 9.3 KB | – |
| plex-web | movies all titleSort deep | 200 | 60/249760 | – | – | 0 | 24ms | 29ms | 47ms | 87.6 KB | 9.3 KB | 9.3 KB | – |
| plex-web | movies all addedAt | 200 | 60/249760 | – | – | 0 | 24ms | 27ms | 51ms | 88.0 KB | 9.4 KB | 9.4 KB | – |
| plex-web | movies all rating | 200 | 60/249760 | – | – | 0 | 22ms | 27ms | 46ms | 87.0 KB | 9.8 KB | 9.8 KB | – |
| plex-web | movies genre filter list | 200 | 19 | – | – | 0 | 2.2ms | 2.5ms | 4.9ms | 2.2 KB | 535 B | 535 B | – |
| plex-web | movies jump bar | 200 | 25 | – | – | 0 | 2.5ms | 3.0ms | 5.2ms | 2.5 KB | 574 B | 574 B | – |
| plex-web | shows all titleSort p1 | 200 | 60/10000 | – | – | 0 | 92ms | 114ms | 183ms | 60.3 KB | 6.9 KB | 6.9 KB | – |
| plex-web | shows allLeaves p1 | 200 | 100/239558 | – | – | 0 | 32ms | 41ms | 58ms | 177.9 KB | 6.6 KB | 6.6 KB | – |
| plex-web | shows allLeaves deep | 200 | 100/239558 | – | – | 0 | 33ms | 40ms | 56ms | 178.5 KB | 6.7 KB | 6.7 KB | – |
| plex-web | shows all type=4 p1 | 200 | 100/239558 | – | – | 0 | 33ms | 41ms | 49ms | 177.9 KB | 6.6 KB | 6.6 KB | – |
| plex-web | shows all type=3 p1 | 200 | 100/39944 | – | – | 0 | 51ms | 61ms | 95ms | 92.6 KB | 5.5 KB | 5.5 KB | – |
| plex-web | shows allLeaves updatedAt | 200 | 100/239558 | – | – | 0 | 35ms | 39ms | 53ms | 177.9 KB | 6.6 KB | 6.6 KB | – |
| plex-web | shows all type=3 updatedAt | 200 | 100/39944 | – | – | 0 | 53ms | 61ms | 104ms | 92.6 KB | 5.5 KB | 5.5 KB | – |
| plex-web | movie detail | 200 | 1 | – | – | 0 | 7.6ms | 8.8ms | 12ms | 2.7 KB | 1.1 KB | 1.1 KB | – |
| plex-web | show detail | 200 | 1 | – | – | 0 | 10ms | 13ms | 19ms | 3.4 KB | 1.1 KB | 1.1 KB | – |
| plex-web | show children | 200 | 4 | – | – | 0 | 9.9ms | 11ms | 19ms | 4.2 KB | 758 B | 758 B | – |
| plex-web | season children | 200 | 6 | – | – | 0 | 12ms | 16ms | 22ms | 11.1 KB | 1.2 KB | 1.2 KB | – |
| plex-web | episode detail | 200 | 1 | – | – | 0 | 7.4ms | 10ms | 11ms | 2.8 KB | 1.1 KB | 1.1 KB | – |
| plex-web | movie postplay | 200 | 0 | – | – | 0 | 4.2ms | 4.8ms | 6.2ms | 91 B | 116 B | 91 B | – |
| plex-web | episode postplay | 200 | 1 | – | – | 0 | 9.8ms | 12ms | 17ms | 2.7 KB | 1.0 KB | 1.0 KB | – |
| plex-web | /hubs/search star | 200 | 17 | – | – | 0 | 68ms | 93ms | 107ms | 51.3 KB | 5.8 KB | 5.8 KB | – |
| plex-web | /library/search star | 200 | 30 | – | – | 0 | 42ms | 51ms | 72ms | 42.9 KB | 4.9 KB | 4.9 KB | – |
| plex-web | /hubs/search the | 200 | 17 | – | – | 0 | 4.2ms | 5.7ms | 8.6ms | 1.9 KB | 360 B | 360 B | – |
| plex-web | /playlists | 200 | 0 | – | – | 0 | 2.6ms | 3.6ms | 4.6ms | 30 B | 55 B | 30 B | – |
| plex-web | /media/providers | 200 | 1 | – | – | 0 | 2.4ms | 2.8ms | 3.5ms | 4.4 KB | 1.4 KB | 1.4 KB | – |
| plex-web | /livetv/dvrs | 404 | 0 | – | – | 0 | 1.7ms | 1.9ms | 3.5ms | 85 B | 110 B | 85 B | – |
| plex-web | live guide grid 3h | 404 | 0 | – | – | 0 | 1.6ms | 2.2ms | 3.0ms | 85 B | 110 B | 85 B | – |
| plex-web | live hubs | 404 | 0 | – | – | 0 | 1.7ms | 2.0ms | 3.0ms | 85 B | 110 B | 85 B | – |
