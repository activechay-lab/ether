package stream

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/usenet"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

// Nested RAR over the real socket → pipelined NNTP → yEnc CRC path, with the
// primary provider losing one article that only the backfill provider has.
func TestNestedRARNNTP(t *testing.T) {
	const password = "Sëcret"
	video := mkvBytes(61, 1_200_000)
	inner := innerRARVolumes(t, video, 350_000)
	plain := nestedRAR(t, video, 5, 350_000, 5, 500_000, newStyle("Movie.2024.1080p"))
	var ms []encMember
	for i, v := range inner {
		ms = append(ms, encMember{name: fmt.Sprintf("Movie.part%02d.rar", i+1), data: v})
	}
	for _, tc := range []struct {
		name, password string
		outer          [][]byte
	}{
		{"plain-outer", "", plain},
		{"encrypted-outer", password, encryptedRAR5(t, password, ms)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := nntptest.Start(t, nntptest.Options{Latency: 2 * time.Millisecond})
			backup := nntptest.Start(t, nntptest.Options{Latency: 2 * time.Millisecond})
			files := map[string][]byte{}
			for i, v := range tc.outer {
				files[fmt.Sprintf("release.part%02d.rar", i+1)] = v
			}
			opts := nntptest.PostOptions{}
			if tc.password != "" {
				opts.Meta = map[string]string{"password": tc.password}
			}
			post, err := primary.Post(files, 8191, opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backup.Post(files, 8191, opts); err != nil {
				t.Fatal(err)
			}
			// An article in the middle of the second outer volume.
			var target string
			for _, f := range post.Files {
				if f.Name == "release.part02.rar" {
					target = f.Segments[len(f.Segments)/2].MessageID
				}
			}
			if target == "" {
				t.Fatal("no target article")
			}
			primary.SetMissing(true, target)
			p, b := primary.Provider("primary", 3), backup.Provider("backup", 2)
			b.Backfill = true
			fetcher := usenet.New([]config.UsenetProvider{p, b}, usenet.Options{MinIdle: -1, KeepAlive: -1, CommandTimeout: 3 * time.Second})
			t.Cleanup(func() { _ = fetcher.Close() })
			e := newTestEngine(t, fetcher, nil, Options{ReadAheadMin: 1, ReadAheadMax: 1})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if ok, err := e.Check(ctx, post.NZB); err != nil || !ok {
				t.Fatalf("Check: %v %v", ok, err)
			}
			media, err := e.Open(ctx, tc.name, post.NZB)
			if err != nil {
				t.Fatal(err)
			}
			defer media.Close()
			if media.Name() != "Movie.2024.1080p.mkv" || media.Size() != int64(len(video)) {
				t.Fatalf("name %q size %d", media.Name(), media.Size())
			}
			src := media.(*source)
			got := make([]byte, len(video))
			if n, err := src.ReadMetadataAtContext(ctx, got, 0); err != nil || n != len(video) || !bytes.Equal(got, video) {
				t.Fatalf("plaintext: %d %v equal=%v", n, err, bytes.Equal(got, video))
			}
			checkRandomReads(t, media, video, 67, 60)
			if backup.BodyRequests(target) == 0 {
				t.Fatal("test did not exercise backfill of a nested-archive article")
			}
		})
	}
}
