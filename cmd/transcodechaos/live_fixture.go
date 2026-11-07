package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// Live fixtures are synthetic H.264 test patterns with a continuous tone. A
// solid green band across the top of every frame marks provider content, so
// the judge can tell provider pictures from Ether's own screens.
//
// Each variant is one continuous TS cut into segments at keyframes. The loop
// period is a multiple of both the 30 fps frame time and the AAC (1024) and
// AC3 (1536) frame times at 48 kHz, so looping with a timestamp offset keeps
// every elementary stream sample-continuous.

type liveVariant struct {
	Name     string
	Size     string
	GOP      int // frames
	Seconds  int // loop period
	SegFrame int // frames per segment
	Audio    string
	// Sync adds a lip-sync pulse every whole second of fixture time: a white
	// flash in a black box in the bottom-right corner for 3 frames, and a
	// 100 ms 3 kHz beep starting at the same instant over the 440 Hz tone.
	Sync bool
}

const liveFPS = 30

var liveVariants = map[string]liveVariant{
	"base":    {Name: "base", Size: "640x360", GOP: 60, Seconds: 64, SegFrame: 120, Audio: "aac"},
	"ac3":     {Name: "ac3", Size: "640x360", GOP: 60, Seconds: 64, SegFrame: 120, Audio: "ac3"},
	"hd":      {Name: "hd", Size: "1280x720", GOP: 60, Seconds: 64, SegFrame: 120, Audio: "aac"},
	"longgop": {Name: "longgop", Size: "640x360", GOP: 300, Seconds: 80, SegFrame: 300, Audio: "aac"},
	"sync":    {Name: "sync", Size: "640x360", GOP: 60, Seconds: 64, SegFrame: 120, Audio: "aac", Sync: true},
}

type liveFixture struct {
	Variant  liveVariant
	Segments [][]byte // each starts with PAT and PMT, then a keyframe
	Program  tsProgram
	VideoPID uint16
	// SegTicks is the segment duration at 90 kHz.
	SegTicks int64
	LoopSegs int
}

var fixtureMu sync.Mutex

// loadLiveFixture generates (or reuses) a variant under cacheDir. The key
// includes the generator arguments and the encoder's version string.
func loadLiveFixture(ctx context.Context, ff, cacheDir string, v liveVariant) (*liveFixture, error) {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	audio := []string{"-c:a", "aac", "-b:a", "128k"}
	if v.Audio == "ac3" {
		audio = []string{"-c:a", "ac3", "-b:a", "192k"}
	}
	frames := v.Seconds * liveFPS
	video := fmt.Sprintf("testsrc2=size=%s:rate=%d,drawbox=x=0:y=0:w=iw:h=ih/12:color=0x00FF00:t=fill", v.Size, liveFPS)
	tone := "sine=frequency=440:sample_rate=48000"
	if v.Sync {
		box := "x=iw*7/8:y=ih*7/8:w=iw/8:h=ih/8"
		video += fmt.Sprintf(",drawbox=%s:color=black:t=fill,drawbox=%s:color=white:t=fill:enable='lt(mod(n,%d),3)'", box, box, liveFPS)
		tone = "aevalsrc=exprs='0.3*sin(2*PI*440*t)+if(lt(mod(t,1),0.1),0.5*sin(2*PI*3000*t),0)':s=48000"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", video,
		"-f", "lavfi", "-i", tone,
		"-frames:v", fmt.Sprint(frames), "-t", fmt.Sprint(v.Seconds + 1),
		"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-pix_fmt", "yuv420p", "-threads", "2",
		"-g", fmt.Sprint(v.GOP), "-keyint_min", fmt.Sprint(v.GOP), "-sc_threshold", "0", "-b:v", "1200k", "-maxrate", "1500k", "-bufsize", "3000k",
	}
	args = append(args, audio...)
	args = append(args, "-ac", "2", "-ar", "48000", "-streamid", "0:256", "-streamid", "1:257", "-f", "mpegts", "-pes_payload_size", "0")
	version, _ := mediaprocOutput(ctx, ff, "-version")
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00") + "\x00" + firstLine(version)))
	dir := filepath.Join(cacheDir, "live-fixtures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, v.Name+"-"+hex.EncodeToString(sum[:6])+".ts")
	if _, err := os.Stat(path); err != nil {
		tmp := path + ".tmp.ts"
		if err := command(ctx, ff, append(args, tmp)...); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return cutFixture(data, v)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// cutFixture strips PSI, trims the loop to exactly v.Seconds per stream, and
// cuts segments at the keyframe that starts each SegFrame-frame span.
func cutFixture(data []byte, v liveVariant) (*liveFixture, error) {
	if len(data)%tsPacket != 0 {
		return nil, fmt.Errorf("fixture is not packet aligned")
	}
	prog, err := parseProgram(data)
	if err != nil {
		return nil, err
	}
	f := &liveFixture{Variant: v, Program: prog, SegTicks: int64(v.SegFrame) * 90000 / liveFPS, LoopSegs: v.Seconds * liveFPS / v.SegFrame}
	var pat, pmt []byte
	for _, s := range prog.Streams {
		if s.Type == 0x1b {
			f.VideoPID = s.PID
		}
	}
	if f.VideoPID == 0 {
		return nil, fmt.Errorf("fixture has no H.264 stream")
	}
	first := map[uint16]int64{}
	dropping := map[uint16]bool{}
	loop := int64(v.Seconds) * 90000
	var cur []byte
	var videoStart int64 = -1
	next := 0
	for i := 0; i < len(data); i += tsPacket {
		p := data[i : i+tsPacket]
		pid := tsPID(p)
		if pid == 0 {
			if pat == nil {
				pat = append([]byte(nil), p...)
			}
			continue
		}
		if pid == prog.PMTPID {
			if pmt == nil {
				pmt = append([]byte(nil), p...)
			}
			continue
		}
		if pid == 0x11 || pid == 0x1fff { // SDT, null
			continue
		}
		if pts, ok := packetPTS(p); ok {
			if _, seen := first[pid]; !seen {
				first[pid] = pts
			}
			dropping[pid] = pts-first[pid] >= loop
			if pid == f.VideoPID && !dropping[pid] && tsRandomAccess(p) {
				if videoStart < 0 {
					videoStart = pts
				}
				if pts-videoStart >= int64(next)*f.SegTicks {
					// Audio muxed ahead of the first keyframe stays in segment 0.
					if next > 0 {
						f.Segments = append(f.Segments, cur)
						cur = append(append([]byte(nil), pat...), pmt...)
					}
					next++
				}
			}
		}
		if cur == nil {
			if pat == nil || pmt == nil {
				continue
			}
			cur = append(append([]byte(nil), pat...), pmt...)
		}
		if dropping[pid] {
			continue
		}
		cur = append(cur, p...)
	}
	if cur != nil {
		f.Segments = append(f.Segments, cur)
	}
	if len(f.Segments) != f.LoopSegs {
		return nil, fmt.Errorf("fixture %s cut into %d segments, want %d", v.Name, len(f.Segments), f.LoopSegs)
	}
	return f, nil
}

func mediaprocOutput(ctx context.Context, exe string, args ...string) (string, error) {
	out, err := mediaproc.CommandContext(ctx, mediaproc.EncodeTimeout, exe, args...).Output()
	return string(out), err
}
