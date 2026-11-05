package textclip

import (
	"testing"
	"unicode/utf8"
)

func TestBytesKeepsUTF8AndBudget(t *testing.T) {
	for _, s := range []string{"ascii", "é日本語🙂"} {
		for n := 0; n <= len(s)+1; n++ {
			got := Bytes(s, n)
			if len(got) > n || !utf8.ValidString(got) {
				t.Fatalf("Bytes(%q,%d)=%q", s, n, got)
			}
			if len(got) < len(s) {
				_, size := utf8.DecodeRuneInString(s[len(got):])
				if len(got)+size <= n {
					t.Fatal("prefix too short")
				}
			}
		}
	}
}
