package transcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
)

type continuousDecoder struct {
	*continuousProcess
	frames   chan RawFrame
	readDone chan struct{}
	readErr  error
	workers  sync.WaitGroup
}

func (m *Manager) StartDecoder(ctx context.Context, r DecoderRequest) (Decoder, error) {
	if e := validateRaw(r.Format); e != nil {
		return nil, e
	}
	// Provider credentials and playlist rewriting belong to livetv's grantInput.
	// Refuse a raw remote input rather than accidentally expose it through argv.
	if len(r.InputHeaders) > 0 {
		return nil, errors.New("transcode: grant input headers through the loopback relay before decoding")
	}
	u, e := url.Parse(r.InputURL)
	if e != nil {
		return nil, errors.New("transcode: invalid decoder input")
	}
	if u.Scheme != "" && !filepath.IsAbs(r.InputURL) {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil {
			return nil, errors.New("transcode: decoder requires a local file or loopback HTTP grant")
		}
	}
	if r.Loop && (r.Live || (u.Scheme != "" && !filepath.IsAbs(r.InputURL))) {
		return nil, errors.New("transcode: Loop requires a local non-live file")
	}
	if r.InputURL == "" || r.Offset < 0 {
		return nil, errors.New("transcode: invalid decoder input or offset")
	}
	sockets := make([]*rawSocket, 0, 2)
	for range 2 {
		s, e := newRawSocket()
		if e != nil {
			for _, s := range sockets {
				s.close()
			}
			return nil, e
		}
		sockets = append(sockets, s)
	}
	ok := false
	defer func() {
		if !ok {
			for _, s := range sockets {
				s.close()
			}
		}
	}()
	f := r.Format
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "2", "-filter_threads", "1"}
	args = append(args, inputSafetyArgs(r.InputURL, r.Live)...)
	if !r.Live && r.Offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(r.Offset.Seconds(), 'f', 6, 64), "-noaccurate_seek")
	}
	if r.Loop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args, "-i", r.InputURL, "-map", "0:v:0", "-vf", fmt.Sprintf("scale=%d:%d,fps=%d/%d,setsar=1,setpts=N*%d/(%d*TB)", f.Width, f.Height, f.FPSNum, f.FPSDen, f.FPSDen, f.FPSNum), "-pix_fmt", "yuv420p", "-c:v", "rawvideo", "-threads:v", "1", "-f", "rawvideo", sockets[0].url(), "-map", "0:a:0", "-af", "aresample=48000:async=1:first_pts=0", "-c:a", "pcm_s16le", "-ar", "48000", "-ac", "2", "-f", "s16le", sockets[1].url())
	p, e := m.startContinuous(ctx, args, "", true, sockets, nil, nil)
	if e != nil {
		return nil, e
	}
	capacity := 8
	if r.Live {
		capacity = 0
	}
	d := &continuousDecoder{continuousProcess: p, frames: make(chan RawFrame, capacity), readDone: make(chan struct{})}
	// Read both sockets independently: muxer scheduling may deliver many audio
	// blocks before the next picture. Bounded queues propagate prewarm pressure.
	vq := make(chan []byte)
	aq := make(chan []byte)
	for i, ch := range []chan []byte{vq, aq} {
		d.workers.Add(1)
		go func(s *rawSocket, ch chan []byte, video bool) {
			defer d.workers.Done()
			defer close(ch)
			c, e := s.accept()
			if e != nil {
				return
			}
			for n := int64(0); ; n++ {
				size := tickPCM(f, n)
				if video {
					size = f.Width * f.Height * 3 / 2
				}
				b := make([]byte, size)
				if _, e := io.ReadFull(c, b); e != nil {
					return
				}
				select {
				case ch <- b:
				case <-p.ctx.Done():
					return
				}
			}
		}(sockets[i], ch, i == 0)
	}
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer close(d.readDone)
		defer close(d.frames)
		for {
			var v, a []byte
			var open bool
			select {
			case v, open = <-vq:
				if !open {
					d.readErr = p.result()
					return
				}
			case <-p.ctx.Done():
				d.readErr = p.result()
				return
			}
			select {
			case a, open = <-aq:
				if !open {
					d.readErr = p.result()
					return
				}
			case <-p.ctx.Done():
				d.readErr = p.result()
				return
			}
			select {
			case d.frames <- RawFrame{Video: v, PCM: a}:
			case <-p.ctx.Done():
				d.readErr = p.result()
				return
			}
		}
	}()
	ok = true
	return d, nil
}
func (d *continuousDecoder) Next(ctx context.Context) (RawFrame, error) {
	select {
	case <-ctx.Done():
		return RawFrame{}, ctx.Err()
	case <-d.ctx.Done():
		return RawFrame{}, d.ctx.Err()
	default:
	}
	select {
	case <-ctx.Done():
		return RawFrame{}, ctx.Err()
	case <-d.ctx.Done():
		return RawFrame{}, d.ctx.Err()
	case f, ok := <-d.frames:
		if !ok {
			return RawFrame{}, d.readErr
		}
		return f, nil
	}
}
func (d *continuousDecoder) Close() error { d.continuousProcess.close(); d.workers.Wait(); return nil }
