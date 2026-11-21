package update

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests sign with their own key; the real one is checked first.
var (
	releasePublicKey = PublicKey
	testKey          *rsa.PrivateKey
)

func TestMain(m *testing.M) {
	var err error
	if testKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		panic(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&testKey.PublicKey)
	PublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	os.Exit(m.Run())
}

func sign(t *testing.T, data []byte) []byte {
	h := sha256.Sum256(data)
	sig, err := rsa.SignPKCS1v15(rand.Reader, testKey, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestReleaseKeyIsUsable(t *testing.T) {
	blk, _ := pem.Decode([]byte(releasePublicKey))
	if blk == nil {
		t.Fatal("no PEM")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if rk, ok := k.(*rsa.PublicKey); err != nil || !ok || rk.N.BitLen() < 3072 {
		t.Fatalf("release key: %v", err)
	}
}

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
	h := strings.Repeat("ab", 32)
	evil := strings.Repeat("cd", 32)
	if got, err := ParseChecksums(h+"  other\n"+h+" *runner-guard_linux_amd64\n", "runner-guard_linux_amd64"); err != nil || got != h {
		t.Fatalf("got %q, %v", got, err)
	}
	for name, text := range map[string]string{
		"missing":       h + "  other\n",
		"vertical tab":  evil + "\vrunner-guard_linux_amd64\n" + h + "  runner-guard_linux_amd64\n",
		"nbsp":          evil + " runner-guard_linux_amd64\n" + h + "  runner-guard_linux_amd64\n",
		"twice":         h + "  runner-guard_linux_amd64\n" + evil + "  runner-guard_linux_amd64\n",
		"short hash":    "abc123  runner-guard_linux_amd64\n",
		"upper-case":    strings.ToUpper(h) + "  runner-guard_linux_amd64\n",
		"trailing junk": h + "  runner-guard_linux_amd64 x\n",
	} {
		if _, err := ParseChecksums(text, "runner-guard_linux_amd64"); err == nil {
			t.Errorf("%s: accepted", name)
		}
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
		case "/sig":
			w.Write(sign(t, []byte(fmt.Sprintf("%s  %s\n", hexSum, asset))))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, Release{Tag: "v0.2.0", Assets: map[string]string{asset: srv.URL + "/bin", "checksums.txt": srv.URL + "/sums", "checksums.txt.sig": srv.URL + "/sig"}}
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

func TestReplaceInstallsAndReinstalls(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rg")
	for _, body := range []string{"one", "two"} {
		n := bin + ".new"
		os.WriteFile(n, []byte(body), 0o755)
		if err := Replace(bin, n); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(bin); string(b) != body {
			t.Fatalf("got %q want %q", b, body)
		}
	}
}

func TestApplyRefusesUnsignedOrWronglySigned(t *testing.T) {
	srv, rel := releaseServer(t, "new", true)
	bin := tempBin(t)
	unsigned := Release{Tag: rel.Tag, Assets: map[string]string{}}
	wrong := Release{Tag: rel.Tag, Assets: map[string]string{}}
	for k, v := range rel.Assets {
		wrong.Assets[k] = v
		if k != "checksums.txt.sig" {
			unsigned.Assets[k] = v
		}
	}
	wrong.Assets["checksums.txt.sig"] = srv.URL + "/bin" // not a signature
	for name, r := range map[string]Release{"unsigned": unsigned, "wrong": wrong} {
		err := Apply(context.Background(), srv.Client(), r, bin, func(string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "not installing") {
			t.Fatalf("%s: %v", name, err)
		}
		if b, _ := os.ReadFile(bin); string(b) != "old" {
			t.Fatalf("%s: binary changed", name)
		}
	}
}

func TestCheckSendsOnlyTheNamedToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(403)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "unrelated")
	t.Setenv("RUNNER_GUARD_GITHUB_TOKEN", "")
	Check(context.Background(), srv.Client(), srv.URL, "o/r")
	if auth != "" {
		t.Fatalf("sent %q without RUNNER_GUARD_GITHUB_TOKEN", auth)
	}
	t.Setenv("RUNNER_GUARD_GITHUB_TOKEN", "tok")
	Check(context.Background(), srv.Client(), srv.URL, "o/r")
	if auth != "Bearer tok" {
		t.Fatalf("got %q", auth)
	}
}
