package disk

import (
	"context"
	"os"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestHostUsedPctReadsRealDrive(t *testing.T) {
	pct, err := hostUsedPct(`C:\`)
	if err != nil || pct <= 0 || pct > 100 {
		t.Fatalf("got %v, %v", pct, err)
	}
}

func TestVHDXPathPrefersConfig(t *testing.T) {
	c := config.Default()
	c.DesktopVHDX = `D:\d\docker_data.vhdx`
	if got := VHDXPath(c); got != c.DesktopVHDX {
		t.Fatalf("got %s", got)
	}
}

func TestReclaimSkipsSmallGain(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	c := config.Default()
	c.DesktopVHDX = f.Name()
	fake := &execx.Fake{Responses: map[string]execx.FakeResponse{
		"docker system df --format {{json .}}": {Out: `{"Size":"1GB"}`},
	}}
	desc, err := Reclaim(context.Background(), fake, c, func() error { t.Fatal("relaunched"); return nil })
	if err != nil || desc != "" {
		t.Fatalf("got %q, %v", desc, err)
	}
	for _, call := range fake.Calls {
		if call[0] == "diskpart" || call[0] == "taskkill" || (len(call) > 1 && call[1] == "desktop") {
			t.Fatalf("stopped docker for nothing: %v", fake.Calls)
		}
	}
}

func TestCompactScriptKeepsPathVerbatim(t *testing.T) {
	got := compactScript(`D:\Docker data\disk\docker_data.vhdx`)
	want := `select vdisk file="D:\Docker data\disk\docker_data.vhdx"` + "\nattach vdisk readonly\ncompact vdisk\ndetach vdisk\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}
