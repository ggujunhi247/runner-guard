// Package update replaces the binary with the latest GitHub release after
// checking its SHA256 and running it.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Version is set at build time with -ldflags "-X .../internal/update.Version=vX.Y.Z".
var Version = "dev"

// ErrSkip means "not today": rate limited, or no asset for this OS.
var ErrSkip = errors.New("update skipped")

const maxDownload = 200 << 20

type Release struct {
	Tag    string
	Assets map[string]string // name -> download URL
}

func parseSemver(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, fmt.Errorf("not a vMAJOR.MINOR.PATCH version: %q", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("not a vMAJOR.MINOR.PATCH version: %q", v)
		}
		out[i] = n
	}
	return out, nil
}

// Newer reports whether latest is a higher semver than current. A "dev"
// build is never updated, and versions never go down.
func Newer(current, latest string) (bool, error) {
	if current == "dev" {
		return false, nil
	}
	c, err := parseSemver(current)
	if err != nil {
		return false, err
	}
	l, err := parseSemver(latest)
	if err != nil {
		return false, err
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i], nil
		}
	}
	return false, nil
}

// ParseChecksums finds asset's hash in sha256sum output.
func ParseChecksums(text, asset string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", asset)
}

func AssetName(goos, goarch string) string {
	name := "runner-guard_" + goos + "_" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func get(ctx context.Context, c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "runner-guard/"+Version)
	return c.Do(req)
}

// Check fetches the latest release. It returns ErrSkip when GitHub rate
// limits us or the release lacks this platform's asset or checksums.txt.
func Check(ctx context.Context, c *http.Client, apiBase, repo string) (Release, error) {
	resp, err := get(ctx, c, strings.TrimRight(apiBase, "/")+"/repos/"+repo+"/releases/latest")
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return Release{}, fmt.Errorf("%w: GitHub answered %s", ErrSkip, resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("release JSON: %w", err)
	}
	rel := Release{Tag: body.TagName, Assets: map[string]string{}}
	for _, a := range body.Assets {
		rel.Assets[a.Name] = a.URL
	}
	for _, need := range []string{AssetName(runtime.GOOS, runtime.GOARCH), "checksums.txt"} {
		if rel.Assets[need] == "" {
			return rel, fmt.Errorf("%w: release %s has no %s", ErrSkip, rel.Tag, need)
		}
	}
	return rel, nil
}

func download(ctx context.Context, c *http.Client, url string, w io.Writer) error {
	resp, err := get(ctx, c, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, io.LimitReader(resp.Body, maxDownload))
	return err
}

// Apply downloads, checks the hash, verifies the new file, swaps it in,
// verifies again, and rolls back if that second check fails.
func Apply(ctx context.Context, c *http.Client, rel Release, binPath string, verify func(path string) error) error {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	var sums strings.Builder
	if err := download(ctx, c, rel.Assets["checksums.txt"], &sums); err != nil {
		return err
	}
	want, err := ParseChecksums(sums.String(), asset)
	if err != nil {
		return err
	}

	newPath := binPath + ".new"
	os.Remove(newPath) // never reuse a file someone else created (and may still own)
	f, err := os.OpenFile(newPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(newPath)
		}
	}()
	h := sha256.New()
	err = download(ctx, c, rel.Assets[asset], io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}
	if err := verify(newPath); err != nil {
		return fmt.Errorf("new binary failed verification: %w", err)
	}
	if err := swap(binPath, newPath); err != nil {
		return err
	}
	ok = true
	if err := verify(binPath); err != nil {
		if rerr := os.Rename(oldPath(binPath), binPath); rerr != nil {
			return fmt.Errorf("installed binary failed verification (%v) and rollback failed: %w", err, rerr)
		}
		return fmt.Errorf("installed binary failed verification, rolled back: %w", err)
	}
	afterSwap(binPath)
	return nil
}

// Cleanup removes the previous binary left by an earlier update.
func Cleanup(binPath string) { os.Remove(oldPath(binPath)) }

// Replace moves newPath over bin, even while bin is running (Windows
// cannot overwrite a running exe). install uses it to reinstall in place.
func Replace(bin, newPath string) error {
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		return os.Rename(newPath, bin)
	}
	if err := swap(bin, newPath); err != nil {
		return err
	}
	afterSwap(bin)
	return nil
}
