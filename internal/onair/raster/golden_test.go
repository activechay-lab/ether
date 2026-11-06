package raster_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/image/draw"
)

// RGBA thumbnails preserve colour and alpha (including transparent overlays).
// SHA256 records the exact full render; small platform differences may pass the
// thumbnail thresholds. DHash guards composition independently of mean error.
type compactGolden struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	PNGBlob string `json:"pngBlob,omitempty"`
	SHA256  string `json:"sha256"`
	DHash   uint64 `json:"dhash"`
	RGBA    []byte `json:"rgba"`
}

const thumbW, thumbH = 32, 18

func compact(im *image.RGBA) compactGolden {
	small := image.NewRGBA(image.Rect(0, 0, thumbW, thumbH))
	draw.BiLinear.Scale(small, small.Bounds(), im, im.Bounds(), draw.Src, nil)
	hashImage := image.NewRGBA(image.Rect(0, 0, 9, 8))
	draw.ApproxBiLinear.Scale(hashImage, hashImage.Bounds(), small, small.Bounds(), draw.Src, nil)
	var hash uint64
	luma := func(x, y int) int {
		i := hashImage.PixOffset(x, y)
		p := hashImage.Pix[i:]
		return 2126*int(p[0]) + 7152*int(p[1]) + 722*int(p[2])
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if luma(x, y) > luma(x+1, y) {
				hash |= 1 << uint(y*8+x)
			}
		}
	}
	return compactGolden{Width: im.Bounds().Dx(), Height: im.Bounds().Dy(), SHA256: fmt.Sprintf("%x", sha256.Sum256(im.Pix)), DHash: hash, RGBA: small.Pix}
}

func compareGolden(got, want compactGolden) error {
	if got.Width != want.Width || got.Height != want.Height || len(want.RGBA) != thumbW*thumbH*4 || len(got.RGBA) != len(want.RGBA) || len(want.SHA256) != 64 {
		return fmt.Errorf("invalid golden dimensions/checksum")
	}
	if got.SHA256 == want.SHA256 {
		return nil
	}
	sum, peak := 0, 0
	for i, v := range got.RGBA {
		d := int(v) - int(want.RGBA[i])
		if d < 0 {
			d = -d
		}
		sum += d
		peak = max(peak, d)
	}
	mean := float64(sum) / float64(len(got.RGBA)*255)
	distance := bits.OnesCount64(got.DHash ^ want.DHash)
	if mean > .005 || peak > 32 || distance > 6 {
		return fmt.Errorf("render regression: RGBA mean %.6f (limit .005), peak %d (32), dhash distance %d (6); full SHA256 got %s expected %s; ONAIR_GOLDEN_DEBUG=1 writes inspection PNGs", mean, peak, distance, got.SHA256, want.SHA256)
	}
	return nil
}

// The full expected frame is deliberately not retained. Enlarge its committed
// thumbnail for inspection; the filename makes the lost detail explicit.
func dumpGolden(t *testing.T, actual *image.RGBA, want compactGolden) {
	t.Helper()
	dir, err := os.MkdirTemp("", "onair-golden-")
	if err != nil {
		t.Fatal(err)
	}
	thumb := &image.RGBA{Pix: want.RGBA, Stride: thumbW * 4, Rect: image.Rect(0, 0, thumbW, thumbH)}
	expected := image.NewRGBA(actual.Bounds())
	draw.NearestNeighbor.Scale(expected, expected.Bounds(), thumb, thumb.Bounds(), draw.Src, nil)
	expectedName := "expected-thumbnail-enlarged.png"
	var expectedImage image.Image = expected
	// Read an existing historical object only on explicit debug. No full PNG is
	// stored in the working tree or added to git by this test.
	if len(want.PNGBlob) == 40 {
		if data, err := exec.Command("git", "cat-file", "blob", want.PNGBlob).Output(); err == nil {
			if decoded, err := png.Decode(bytes.NewReader(data)); err == nil {
				rgba := image.NewRGBA(decoded.Bounds())
				draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
				if compact(rgba).SHA256 == want.SHA256 {
					expectedImage = rgba
					expectedName = "expected.png"
				}
			}
		}
	}
	for name, im := range map[string]image.Image{"actual.png": actual, expectedName: expectedImage} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, im)
		closeErr := f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	t.Logf("golden inspection images: %s (baseline: %s)", dir, expectedName)
}
