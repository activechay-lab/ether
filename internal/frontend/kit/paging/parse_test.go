package paging

import (
	"strconv"
	"testing"
)

func TestQueryBounds(t *testing.T) {
	if n, e := ParseLimit("", 20, 100); n != 20 || e != nil {
		t.Fatal(n, e)
	}
	for _, raw := range []string{"-1", strconv.Itoa(int(^uint(0) >> 1))} {
		if _, e := ParseOffset(raw, 50); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	off := PageOffset(int(^uint(0)>>1), 100)
	if off < 0 || off+100 < off {
		t.Fatal(off)
	}
}
