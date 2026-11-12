package builtin

import (
	"github.com/braedonsaunders/ether/internal/livetv"
	"strings"
	"testing"
)

func TestChannelApplyValidation(t *testing.T) {
	valid := livetv.Channel{Name: "Cinema", Number: "10", Kind: "virtual", Blocks: []livetv.Block{{Start: 0, End: 720}, {Start: 720, End: 1440}}}
	if e := validateChannelDraft(valid, []string{"https://youtu.be/abc"}); e != nil {
		t.Fatal(e)
	}
	for _, links := range [][]string{{"http://youtube.com/watch?v=a"}, {"https://youtube.com.evil.test/a"}, {"https://user@youtube.com/a"}, strings.Fields(strings.Repeat("https://youtu.be/a ", 31))} {
		if validateChannelDraft(valid, links) == nil {
			t.Fatal("accepted unsafe or oversized links")
		}
	}
	valid.Blocks[1].Start = 700
	if validateChannelDraft(valid, nil) == nil {
		t.Fatal("accepted overlapping blocks")
	}
	valid.Blocks[1].Days = []int{8}
	if validateChannelDraft(valid, nil) == nil {
		t.Fatal("accepted invalid weekday")
	}
}
