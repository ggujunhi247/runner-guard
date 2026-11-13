package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{{"v0.1.0", "v0.2.0", true}, {"v0.2.0", "v0.1.9", false}, {"v1.0.0", "v1.0.0", false}, {"dev", "v9.0.0", false}, {"v0.9.0", "v0.10.0", true}}
	for _, c := range cases {
		got, err := Newer(c.cur, c.latest)
		if err != nil || got != c.want {
			t.Errorf("Newer(%s,%s) = %v, %v", c.cur, c.latest, got, err)
		}
	}
	if _, err := Newer("v0.1.0", "garbage"); err == nil {
		t.Error("want error for garbage")
	}
}

func TestParseChecksums(t *testing.T) {
	got, err := ParseChecksums("abc123  runner-guard_linux_amd64\n", "runner-guard_linux_amd64")
	if err != nil || got != "abc123" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ParseChecksums("abc123  other\n", "runner-guard_linux_amd64"); err == nil {
		t.Fatal("want error for missing asset")
	}
}

func TestCheckRateLimitedSkips(t *testing.T) {
	for _, code := range []int{403, 429} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		_, err := Check(context.Background(), srv.Client(), srv.URL, "o/r")
		srv.Close()
		if !errors.Is(err, ErrSkip) {
			t.Errorf("%d: got %v", code, err)
		}
	}
}

func TestCheckMissingAssetSkips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.2.0","assets":[{"name":"checksums.txt","browser_download_url":"x"},{"name":"runner-guard_plan9_amd64","browser_download_url":"y"}]}`)
	}))
	defer srv.Close()
	if _, err := Check(context.Background(), srv.Client(), srv.URL, "o/r"); !errors.Is(err, ErrSkip) {
		t.Fatalf("got %v", err)
	}
}

func TestCheckFindsRelease(t *testing.T) {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases/latest" || r.UserAgent() != "runner-guard/"+Version {
			w.WriteHeader(404)
			return
		}
		fmt.Fprintf(w, `{"tag_name":"v0.2.0","assets":[{"name":"checksums.txt","browser_download_url":"c"},{"name":%q,"browser_download_url":"a"}]}`, asset)
	}))
	defer srv.Close()
	rel, err := Check(context.Background(), srv.Client(), srv.URL, "o/r")
	if err != nil || rel.Tag != "v0.2.0" || rel.Assets[asset] != "a" {
		t.Fatalf("got %+v, %v", rel, err)
	}
}

// releaseServer serves a new binary and a checksums.txt (correct or not).
func releaseServer(t *testing.T, body string, goodSum bool) (*httptest.Server, Release) {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256([]byte(body))
	hexSum := hex.EncodeToString(sum[:])
	if !goodSum {
		hexSum = "00" + hexSum[2:]
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			fmt.Fprint(w, body)
		case "/sums":
			fmt.Fprintf(w, "%s  %s\n", hexSum, asset)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, Release{Tag: "v0.2.0", Assets: map[string]string{asset: srv.URL + "/bin", "checksums.txt": srv.URL + "/sums"}}
}

func tempBin(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "runner-guard")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	os.WriteFile(p, []byte("old"), 0o755)
	return p
}

func TestApplyRejectsBadChecksum(t *testing.T) {
	srv, rel := releaseServer(t, "new", false)
	bin := tempBin(t)
	if err := Apply(context.Background(), srv.Client(), rel, bin, func(string) error { return nil }); err == nil {
		t.Fatal("want checksum error")
	}
	if b, _ := os.ReadFile(bin); string(b) != "old" {
		t.Fatalf("binary changed: %q", b)
	}
	if _, err := os.Stat(bin + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new left behind")
	}
}

func TestApplyRollsBackWhenVerifyFails(t *testing.T) {
	srv, rel := releaseServer(t, "new", true)
	bin := tempBin(t)
	calls := 0
	verify := func(string) error {
		calls++
		if calls == 2 {
			return errors.New("broken")
		}
		return nil
	}
	if err := Apply(context.Background(), srv.Client(), rel, bin, verify); err == nil {
		t.Fatal("want error")
	}
	if b, _ := os.ReadFile(bin); string(b) != "old" {
		t.Fatalf("not rolled back: %q", b)
	}
}

func TestApplySwaps(t *testing.T) {
	srv, rel := releaseServer(t, "new", true)
	bin := tempBin(t)
	if err := Apply(context.Background(), srv.Client(), rel, bin, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "new" {
		t.Fatalf("got %q", b)
	}
	Cleanup(bin)
	if _, err := os.Stat(oldPath(bin)); !os.IsNotExist(err) {
		t.Fatal("old binary left after Cleanup")
	}
}
