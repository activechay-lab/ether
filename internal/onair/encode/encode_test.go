package encode

import (
	"context"
	"errors"
	"image"
	"os"
	"runtime"
	"testing"
	"time"
)

// A real subprocess that never reads input exercises cancellation of a blocked
// rawvideo pipe, worker teardown and reaping, without requiring installed ffmpeg.
func TestMain(m *testing.M) {
	if os.Getenv("ONAIR_ENCODE_HELPER") == "1" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestCancellation(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("subprocess unavailable")
	}
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("ONAIR_ENCODE_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	e = WithWorkers(ctx, bin, func(int) *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 1920, 1080)) }, 120, 30, "", t.TempDir()+"/clip.mp4", 2)
	if e == nil {
		t.Fatal("cancellation succeeded unexpectedly")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("slow cancellation")
	}
}
