// onair-bench measures a full eight-second master, including rendering and encoding.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/encode"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"image"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	family := flag.String("family", "late-night", "family")
	moment := flag.String("moment", "ident", "moment")
	out := flag.String("out", "/tmp/onair-master.mp4", "output")
	width := flag.Int("width", 1920, "render width")
	ceiling := flag.String("maxrate", "", "bitrate ceiling, e.g. 4M")
	hash := flag.Bool("hash", false, "print RGBA hash at t=3")
	still := flag.Bool("still", false, "render poster PNG only")
	flag.Parse()
	doc, e := families.Get(*family, *moment)
	if e != nil {
		panic(e)
	}
	bind := raster.Bindings{Bindings: families.Defaults(*family)}
	if *hash {
		im := raster.Render(doc, bind, 3, *width, *width*9/16)
		fmt.Printf("%x\n", sha256.Sum256(im.Pix))
		return
	}
	if *still {
		f, e := os.Create(*out)
		if e != nil {
			panic(e)
		}
		defer f.Close()
		if e = png.Encode(f, raster.Render(doc, bind, doc.Poster, *width, *width*9/16)); e != nil {
			panic(e)
		}
		return
	}
	bin, e := exec.LookPath("ffmpeg")
	if e != nil {
		panic(e)
	}
	start := time.Now()
	e = encode.WithOptions(context.Background(), bin, func(i int) *image.RGBA { return raster.Render(doc, bind, float64(i)/30, *width, *width*9/16) }, int(doc.Duration)*30, 30, "", *out, encode.Options{Workers: 4, Width: *width, Height: *width * 9 / 16, MaxRate: *ceiling, BufferSize: *ceiling, Audio: doc.Audio})
	if e != nil {
		panic(e)
	}
	elapsed := time.Since(start)
	fmt.Printf("%s/%s: clip=%gs frames=%d workers=4 GOMAXPROCS=%d wall=%s ratio=%.3fx\n", *family, *moment, doc.Duration, int(doc.Duration)*30, runtime.GOMAXPROCS(0), elapsed, float64(elapsed)/float64(time.Duration(doc.Duration)*time.Second))
}
