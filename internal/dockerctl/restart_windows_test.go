package dockerctl

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestRestartWindowsPrefersDesktopCLI(t *testing.T) {
	f := &execx.Fake{}
	if err := Restart(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"docker", "desktop", "version"}, {"docker", "desktop", "restart"}}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestRestartWindowsFallback(t *testing.T) {
	f := &execx.Fake{Responses: map[string]execx.FakeResponse{
		"docker desktop version": {Err: errors.New("unknown command")},
	}}
	if err := Restart(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"docker", "desktop", "version"},
		{"taskkill", "/F", "/IM", "Docker Desktop.exe"},
		{"schtasks", "/Run", "/TN", "runner-guard-desktop"},
	}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestRestartWindowsLaunchesWhenDesktopRestartFails(t *testing.T) {
	f := &execx.Fake{Responses: map[string]execx.FakeResponse{
		"docker desktop restart": {Err: errors.New("Docker Desktop is not running")},
	}}
	if err := Restart(context.Background(), f); err != nil || f.Calls[len(f.Calls)-1][0] != "schtasks" {
		t.Fatalf("%v %v", err, f.Calls)
	}
}
