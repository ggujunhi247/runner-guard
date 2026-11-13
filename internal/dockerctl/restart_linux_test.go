package dockerctl

import (
	"context"
	"reflect"
	"testing"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

func TestRestartLinux(t *testing.T) {
	f := &execx.Fake{}
	if err := Restart(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Calls, [][]string{{"systemctl", "restart", "docker"}}) {
		t.Fatalf("calls %v", f.Calls)
	}
}
