package disk

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestRemoveStuckOnlyOldRunnerContainers(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	f := &execx.Fake{Responses: map[string]execx.FakeResponse{
		"docker ps -q --no-trunc --filter " + RunnerLabel + " --filter status=running": {Out: "old\nfresh\n"},
		"docker inspect --format {{.Id}} {{.State.StartedAt}} old fresh": {
			Out: "old 2026-11-16T03:00:00.123456789Z\nfresh 2026-11-16T11:30:00Z\n"},
	}}
	removed, err := RemoveStuck(context.Background(), f, now, 6*time.Hour)
	if err != nil || !reflect.DeepEqual(removed, []string{"old"}) {
		t.Fatalf("got %v, %v", removed, err)
	}
	last := f.Calls[len(f.Calls)-1]
	if !reflect.DeepEqual(last, []string{"docker", "rm", "--force", "old"}) {
		t.Fatalf("last call %v", last)
	}
}

func TestRemoveStuckNothingRunning(t *testing.T) {
	f := &execx.Fake{}
	removed, err := RemoveStuck(context.Background(), f, time.Now(), 6*time.Hour)
	if err != nil || removed != nil || len(f.Calls) != 1 {
		t.Fatalf("got %v, %v, calls %v", removed, err, f.Calls)
	}
}

func TestStaleIDsRejectsGarbage(t *testing.T) {
	if _, err := staleIDs("abc not-a-time\n", time.Now(), time.Hour); err == nil {
		t.Fatal("want error")
	}
}
