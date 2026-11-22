package update

import (
	"regexp"
	"strings"
	"testing"
)

func FuzzParseChecksums(f *testing.F) {
	h := strings.Repeat("ab", 32)
	f.Add(h+"  runner-guard_linux_amd64\n", "runner-guard_linux_amd64")
	f.Add(h+" *a\n"+h+"  b\n", "b")
	f.Add("garbage", "x")
	f.Fuzz(func(t *testing.T, text, asset string) {
		got, err := ParseChecksums(text, asset)
		if err != nil {
			return
		}
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
			t.Fatalf("hash %q is not 64 lowercase hex", got)
		}
		ok := false
		for _, l := range strings.Split(text, "\n") {
			ok = ok || l == got+"  "+asset || l == got+" *"+asset
		}
		if !ok {
			t.Fatalf("hash %q for %q is not on an exact checksum line in %q", got, asset, text)
		}
	})
}
