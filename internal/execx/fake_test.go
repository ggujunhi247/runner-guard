package execx

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestFakeRecordsCallsAndReturnsResponse(t *testing.T) {
	f := &Fake{Responses: map[string]FakeResponse{"docker info": {Out: "ok"}}}
	out, err := f.Run(context.Background(), time.Second, "docker", "info")
	if out != "ok" || err != nil {
		t.Fatalf("got %q, %v", out, err)
	}
	if !reflect.DeepEqual(f.Calls, [][]string{{"docker", "info"}}) {
		t.Fatalf("calls %v", f.Calls)
	}
	out, err = f.Run(context.Background(), time.Second, "nope")
	if out != "" || err != nil {
		t.Fatalf("unknown: got %q, %v", out, err)
	}
}
