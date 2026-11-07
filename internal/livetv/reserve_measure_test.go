package livetv

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReserveMeasure plays a real transport stream from a fake live provider
// through a Steady grant's tap, with a reader paced in real time like
// Steady's frame clock, and reports reserve depth, stalls and freezes.
// Opt-in: ETHER_RESERVE_FIXTURE=<ts file> (and ETHER_IPTV_FFMPEG to check
// the delivered stream decodes cleanly).
func TestReserveMeasure(t *testing.T) {
	fixture := os.Getenv("ETHER_RESERVE_FIXTURE")
	if fixture == "" {
		t.Skip("set ETHER_RESERVE_FIXTURE to a TS file to measure the reserve")
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	// Media time of every indexed offset in the fixture.
	sp := newTSSplicer()
	_, marks := sp.feed(data, nil, 0)
	if len(marks) < 10 {
		t.Fatal("fixture has no PCR index")
	}
	offAt := func(d time.Duration) int {
		c := marks[0].clock + int64(d/time.Millisecond)*tsClockHz/1000
		i := sort.Search(len(marks), func(i int) bool { return marks[i].clock > c })
		if i == 0 {
			return 0
		}
		return int(marks[i-1].off)
	}
	length := time.Duration(marks[len(marks)-1].clock-marks[0].clock) * time.Second / tsClockHz
	type scenario struct {
		name              string
		burst             time.Duration // provider keeps this much behind live and sends it at once
		dropAt            time.Duration // provider closes the connection
		stallAt, stallFor time.Duration // provider stops sending, connection open
		// copy models a probe followed by a relay ffmpeg in copy mode: the
		// probe reads 1 MiB and leaves, then the reader takes every byte it
		// is released (Direct's owned relay on a real-time grant).
		copy bool
		run  time.Duration
	}
	scenarios := []scenario{
		{name: "steady-no-burst", run: 20 * time.Second},
		{name: "burst5", burst: 5 * time.Second, run: 20 * time.Second},
		{name: "burst5-drop", burst: 5 * time.Second, dropAt: 12 * time.Second, run: 25 * time.Second},
		{name: "burst8-stall6", burst: 8 * time.Second, stallAt: 12 * time.Second, stallFor: 6 * time.Second, run: 28 * time.Second},
		{name: "no-burst-drop", dropAt: 12 * time.Second, run: 25 * time.Second},
		{name: "copy-burst8-stall6", burst: 8 * time.Second, stallAt: 12 * time.Second, stallFor: 6 * time.Second, run: 28 * time.Second, copy: true},
	}
	if only := os.Getenv("ETHER_RESERVE_SCENARIO"); only != "" {
		var keep []scenario
		for _, sc := range scenarios {
			if sc.name == only {
				keep = append(keep, sc)
			}
		}
		scenarios = keep
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			if sc.run > length {
				t.Fatalf("fixture is %v, scenario needs %v", length, sc.run)
			}
			var mu sync.Mutex
			var opens int
			t0 := time.Now()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				opens++
				n := opens
				mu.Unlock()
				w.Header().Set("Content-Type", "video/mp2t")
				// The channel has been live for a while: its edge is 10 s into
				// the fixture at t0, and a new connection starts burst behind it.
				const live = 10 * time.Second
				edge := live + time.Since(t0)
				pos := offAt(max(0, edge-sc.burst))
				pos -= pos % tsPacket
				flusher := w.(http.Flusher)
				for {
					now := time.Since(t0)
					if n == 1 && sc.dropAt > 0 && now >= sc.dropAt {
						return
					}
					if sc.stallFor > 0 && now >= sc.stallAt && now < sc.stallAt+sc.stallFor {
						select {
						case <-r.Context().Done():
							return
						case <-time.After(20 * time.Millisecond):
						}
						continue
					}
					end := offAt(live + now)
					end -= end % tsPacket
					if end > pos {
						if _, err := w.Write(data[pos:end]); err != nil {
							return
						}
						flusher.Flush()
						pos = end
					}
					select {
					case <-r.Context().Done():
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
			}))
			defer up.Close()
			s := testService(t, Options{HTTPClient: up.Client(), Stability: func() string { return stabilityBalanced }})
			c := Channel{ID: "m", Kind: "iptv"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "steady-m", Channel: c}}
			if err := s.grantSteadyInput(ss, StreamSource{ID: "a", URL: up.URL + "/live/1.ts"}, http.Header{}); err != nil {
				t.Fatal(err)
			}
			defer s.revokeInput(ss)
			var events []string
			var evMu sync.Mutex
			input, err := s.relayInput(ss)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			if sc.copy {
				probe, err := http.Get(input)
				if err != nil {
					t.Fatal(err)
				}
				io.CopyN(io.Discard, probe.Body, 1<<20)
				probe.Body.Close()
				time.Sleep(50 * time.Millisecond)
			}
			res, err := http.Get(input)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			stop := s.watchInput(ss, func(ev UpstreamEvent) {
				evMu.Lock()
				events = append(events, fmt.Sprintf("%s@%.1fs(depth %.1fs)", ev.Kind, time.Since(t0).Seconds(), ev.Health.ReserveDepth.Seconds()))
				evMu.Unlock()
			})
			defer stop()
			// Paced reader: plays from its first keyframe at real time, and
			// buffers at most 1 s ahead like a decoder. An underrun is a freeze.
			rsp := newTSSplicer()
			var got []byte
			var firstByte, playStart, firstKey time.Time
			var playClock int64 = -1
			var freeze, maxFreeze, curFreeze time.Duration // stall time after the first picture
			lastLoop := time.Now()
			keyAt := int64(-1)
			var depths []string
			lastSample := time.Now()
			buf := make([]byte, tsPacket*16)
			deadline := started.Add(sc.run)
			body := res.Body
			type chunk struct {
				b   []byte
				err error
			}
			chunks := make(chan chunk, 1)
			go func() {
				for {
					n, err := body.Read(buf)
					b := append([]byte(nil), buf[:n]...)
					chunks <- chunk{b, err}
					if err != nil {
						return
					}
				}
			}()
			for time.Now().Before(deadline) {
				// How much media the reader holds beyond the playhead.
				ahead := time.Duration(0)
				step := time.Since(lastLoop)
				lastLoop = time.Now()
				if playClock >= 0 && !playStart.IsZero() {
					played := time.Since(playStart)
					have := time.Duration(rsp.clock-playClock) * time.Second / tsClockHz
					ahead = have - played
					if ahead < 0 {
						// The player stalls: its playhead waits for data.
						playStart = playStart.Add(step)
						curFreeze += step
						freeze += step
						maxFreeze = max(maxFreeze, curFreeze)
					} else {
						curFreeze = 0
					}
				}
				if time.Since(lastSample) >= time.Second {
					lastSample = time.Now()
					if h, ok := s.inputHealth(ss); ok {
						depths = append(depths, fmt.Sprintf("%.0f:%.1f", time.Since(t0).Seconds(), h.ReserveDepth.Seconds()))
						if os.Getenv("ETHER_RESERVE_DEBUG") != "" {
							g := s.sessionGrant(ss)
							g.mu.Lock()
							tp := g.tap
							g.mu.Unlock()
							tp.mu.Lock()
							for rd := range tp.active {
								t.Logf("rd off %d written %d head %d clockAt %d marks %d pace %v started %v", rd.off, tp.written, tp.headClockLocked(), tp.clockAtLocked(rd.off), len(tp.marks), rd.pace != nil, rd.pace != nil && rd.pace.anchored)
							}
							tp.mu.Unlock()
							t.Logf("health %+v", h)
						}
					}
				}
				if ahead > time.Second && !sc.copy {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				select {
				case ch := <-chunks:
					if len(ch.b) > 0 {
						if firstByte.IsZero() {
							firstByte = time.Now()
						}
						_, m := rsp.feed(ch.b, nil, int64(len(got)))
						got = append(got, ch.b...)
						if playClock < 0 {
							for _, mk := range m {
								if mk.key {
									// Like a decoder, play once a second is buffered.
									playClock, playStart, keyAt = mk.clock, time.Now().Add(time.Second), mk.entry
									firstKey = time.Now()
									break
								}
							}
						}
					}
					if ch.err != nil {
						t.Logf("reader ended: %v", ch.err)
						deadline = time.Now()
					}
				case <-time.After(10 * time.Millisecond):
				}
			}
			mu.Lock()
			n := opens
			mu.Unlock()
			evMu.Lock()
			t.Logf("%s: first byte %.3fs, first keyframe %.3fs, provider opens %d, freeze total %.2fs max %.2fs",
				sc.name, firstByte.Sub(started).Seconds(), firstKey.Sub(started).Seconds(), n, freeze.Seconds(), maxFreeze.Seconds())
			t.Logf("%s: events %s", sc.name, strings.Join(events, " "))
			t.Logf("%s: depth(s) by second %s", sc.name, strings.Join(depths, " "))
			evMu.Unlock()
			// The delivered bytes are one continuous stream.
			seen := scanOut(t, got[:len(got)-len(got)%tsPacket])
			lastCC := map[int]byte{}
			var lastV int64 = -1
			for i, x := range seen {
				if prev, ok := lastCC[x.pid]; ok && x.payload && x.cc != (prev+1)&0x0f && x.cc != prev {
					t.Errorf("packet %d pid %#x continuity %d after %d", i, x.pid, x.cc, prev)
					break
				}
				lastCC[x.pid] = x.cc
				if x.pid == 0x100 && x.hasTS {
					if lastV >= 0 && (x.dts <= lastV || x.dts-lastV > 9000) {
						t.Errorf("packet %d video DTS step %d", i, x.dts-lastV)
					}
					lastV = x.dts
				}
			}
			// Decode from the first keyframe (and the PAT before it), as a
			// decoder joining the stream would.
			keyAt = 0
			for i, x := range seen {
				if x.pid == 0x100 && x.key {
					for j := i; j >= 0 && j > i-20; j-- {
						if seen[j].pid == 0 && seen[j].pusi {
							i = j
							break
						}
					}
					keyAt = int64(i * tsPacket)
					break
				}
			}
			if ff := os.Getenv("ETHER_IPTV_FFMPEG"); ff != "" {
				out := t.TempDir() + "/got.ts"
				// Stop before the last frame, which the test's end cut short.
				end := len(got) - len(got)%tsPacket
				for i := len(seen) - 1; i >= 0; i-- {
					if seen[i].pid == 0x100 && seen[i].pusi {
						end = i * tsPacket
						break
					}
				}
				os.WriteFile(out, got[max(keyAt, 0):end], 0o600)
				cmd := exec.Command(ff, "-v", "error", "-i", out, "-f", "null", "-")
				msg, _ := cmd.CombinedOutput()
				lines := strings.Count(strings.TrimSpace(string(msg)), "\n")
				if len(strings.TrimSpace(string(msg))) > 0 {
					lines++
				}
				t.Logf("%s: ffmpeg decode of %d delivered bytes: %d error lines %q", sc.name, len(got), lines, firstLines(string(msg), 3))
			}
			_ = io.Discard
		})
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(strings.TrimSpace(s), "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, " | ")
}
