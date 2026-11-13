package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
