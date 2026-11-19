package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendPostsTextAndContent(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL, "hi"); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "hi" || got["content"] != "hi" {
		t.Fatalf("got %v", got)
	}
}

func TestSendReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL, "hi"); err == nil {
		t.Fatal("want error")
	}
}

func TestSendErrorHidesURL(t *testing.T) {
	const secret = "http://localhost:1/hooks/SECRET-TOKEN"
	err := Send(context.Background(), secret, "hi")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error leaks the URL: %v", err)
	}
}
