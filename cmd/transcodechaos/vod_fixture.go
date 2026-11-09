package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The VOD sync fixture is a library-style Matroska title that lets AVPlayer's
// picture be mapped back to content time: every frame carries a 16-bit frame
// counter as a barcode (a white guard box, then 16 boxes LSB first, on a black
// strip at 70-78% of the height), the live fixtures' green top band, and the
// lip-sync pulse (a white corner flash and a 3 kHz beep at every whole
// second). Two audio tracks (AAC stereo, AC-3 5.1) and an SRT track exercise
// track changes. 90 s at 1280x720/30 with a closed 4 s GOP.

const (
	vodSyncName    = "corpus-ether-sync.mkv"
	vodSyncSeconds = 90
)

func vodSyncFilter() string {
	f := []string{
		fmt.Sprintf("testsrc2=size=1280x720:rate=%d", liveFPS),
		"drawbox=x=0:y=0:w=iw:h=ih/12:color=0x00FF00:t=fill",
		"drawbox=x=iw*7/8:y=ih*7/8:w=iw/8:h=ih/8:color=black:t=fill",
		fmt.Sprintf("drawbox=x=iw*7/8:y=ih*7/8:w=iw/8:h=ih/8:color=white:t=fill:enable='lt(mod(n,%d),3)'", liveFPS),
		"drawbox=x=iw*0.02:y=ih*0.70:w=iw*0.92:h=ih*0.08:color=black:t=fill",
		"drawbox=x=iw*0.028:y=ih*0.71:w=iw*0.04:h=ih*0.06:color=white:t=fill",
	}
	for i := 0; i < 16; i++ {
		f = append(f, fmt.Sprintf("drawbox=x=iw*%.3f:y=ih*0.71:w=iw*0.04:h=ih*0.06:color=white:t=fill:enable='eq(mod(floor(n/%d),2),1)'", 0.08+0.052*float64(i), 1<<i))
	}
	return strings.Join(f, ",")
}

// stageVODSyncFixture writes the fixture into dir as the corpus scenario
// tests expect (name.good probed, name.media served), once per ffmpeg build.
func stageVODSyncFixture(ctx context.Context, ff, dir string) error {
	good := filepath.Join(dir, vodSyncName+".good")
	media := filepath.Join(dir, vodSyncName+".media")
	if st, err := os.Stat(media); err == nil && st.Size() > 0 {
		return nil
	}
	srt := filepath.Join(dir, "ether-sync.srt")
	var b strings.Builder
	for i := 0; i < vodSyncSeconds/5; i++ {
		fmt.Fprintf(&b, "%d\n00:%02d:%02d,000 --> 00:%02d:%02d,500\nSubtitle %d\n\n", i+1, i*5/60, i*5%60, i*5/60, i*5%60+2, i+1)
	}
	if err := os.WriteFile(srt, []byte(b.String()), 0o600); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "ether-sync.tmp.mkv")
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", vodSyncFilter(),
		"-f", "lavfi", "-i", "aevalsrc=exprs='0.3*sin(2*PI*440*t)+if(lt(mod(t,1),0.1),0.5*sin(2*PI*3000*t),0)':s=48000",
		"-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "1:a", "-map", "2:s",
		"-frames:v", fmt.Sprint(vodSyncSeconds * liveFPS), "-t", fmt.Sprint(vodSyncSeconds),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", "2",
		"-g", "120", "-keyint_min", "120", "-sc_threshold", "0",
		"-c:a:0", "aac", "-b:a:0", "128k", "-ac:a:0", "2",
		"-c:a:1", "ac3", "-b:a:1", "384k", "-ac:a:1", "6",
		"-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=fre", "-metadata:s:s:0", "language=eng",
		tmp}
	if err := command(ctx, ff, args...); err != nil {
		return err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	_ = os.Remove(tmp)
	if err := os.WriteFile(good, data, 0o600); err != nil {
		return err
	}
	return os.WriteFile(media, data, 0o600)
}
