package tune

import (
	"regexp"
	"strings"
	"testing"
)

func FuzzSetWSL(f *testing.F) {
	f.Add([]byte("[wsl2]\nmemory=4GB\nswap=2GB\n"), false, 8)
	f.Add([]byte(""), true, 1)
	f.Add([]byte("[boot]\nsystemd=true\n[wsl2]\nprocessors=2\n[experimental]\nx=1"), false, 6)
	f.Add([]byte("\xef\xbb\xbf[wsl2]\r\nMemory = 512MB\r\n"), false, 3) // regression: capitalised key
	f.Fuzz(func(t *testing.T, in []byte, swap bool, gb int) {
		key := "memory"
		if swap {
			key = "swap"
		}
		gb = (gb%1000001 + 1000001) % 1000001 // WSLValue reads only non-negative whole GB
		out, err := SetWSL(in, key, gb)
		if err != nil {
			return
		}
		if got := WSLValue(out, key); got != float64(gb) {
			t.Fatalf("WSLValue(out)=%v want %d\nin=%q\nout=%q", got, gb, in, out)
		}
		// every other non-blank input line survives; only a key line may be replaced
		keyRe := regexp.MustCompile(`(?i)^\s*` + key + `\s*=`)
		have := map[string]int{}
		for _, l := range strings.Split(string(out), "\n") {
			have[strings.TrimSpace(l)]++
		}
		for _, l := range strings.Split(strings.TrimPrefix(string(in), "\xef\xbb\xbf"), "\n") {
			s := strings.TrimSpace(l)
			if s == "" || keyRe.MatchString(l) {
				continue
			}
			if have[s]--; have[s] < 0 {
				t.Fatalf("line %q lost\nin=%q\nout=%q", s, in, out)
			}
		}
	})
}

func FuzzWSLValue(f *testing.F) {
	f.Add([]byte("[wsl2]\nmemory=4GB\n"), "memory")
	f.Add([]byte("[WSL2]\nswap = 512mb"), "swap")
	f.Fuzz(func(t *testing.T, in []byte, key string) { WSLValue(in, key) })
}

func FuzzParseDockerOutput(f *testing.F) {
	f.Add("abc123 12.3MiB / 7.6GiB\ndef 1GiB / 2GiB\n")
	f.Add("abc 42\ndef\nghi 1 2 3")
	f.Add("MemTotal: 123 kB\npswpin 5")
	f.Fuzz(func(t *testing.T, s string) {
		parseJobIDs(s)
		parseStats(s)
		parseKV(s)
	})
}
