package config

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzLoad(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte("\xef\xbb\xbf{\"update_repo\":\"a/b\"}"))
	f.Add([]byte("\xff\xfe{\x00}\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		Load(p)
	})
}
