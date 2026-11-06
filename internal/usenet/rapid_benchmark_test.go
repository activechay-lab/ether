package usenet

import (
	"bufio"
	"bytes"
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/yenc"
)

// Includes the NNTP body reader, full framing and CRC for both decoders.
func BenchmarkBodyDecode(b *testing.B) {
	for _, line := range []int{128, 256} {
		for _, size := range []int{384 << 10, 768 << 10} {
			data := randData(95, size)
			article, err := yenc.EncodeBytes(data, yenc.EncodeOptions{Name: "bench.bin", LineLength: line})
			if err != nil {
				b.Fatal(err)
			}
			wire := append(article, []byte(".\r\n")...)
			for _, swar := range []bool{false, true} {
				b.Run(fmt.Sprintf("line%d/%dKiB/SWAR%v", line, size>>10, swar), func(b *testing.B) {
					input := bytes.NewReader(wire)
					c := &Conn{r: bufio.NewReaderSize(input, readBufferSize)}
					end := func(err error) error { return err }
					b.SetBytes(int64(size))
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						input.Reset(wire)
						c.r.Reset(input)
						br := &BodyReader{c: c, end: end}
						var err error
						if swar {
							_, err = yenc.DecodeLines(br, nil)
						} else {
							_, err = decodeBody(br, nil)
						}
						if err != nil {
							b.Fatal(err)
						}
						if err = br.Close(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
