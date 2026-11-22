package push

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendOK(t *testing.T) {
	var gotMethod, gotPath, gotToken, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Client-Token")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body) //nolint:errcheck // test server
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), srv.URL, "sensor-1", "tok123", []byte(`{"healthy":true}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/sensor-1" {
		t.Errorf("path = %q", gotPath)
	}
	if gotToken != "tok123" {
		t.Errorf("token header = %q", gotToken)
	}
	if gotCT != "application/json" {
		t.Errorf("content type = %q", gotCT)
	}
	if gotBody != `{"healthy":true}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestSendOKAny2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New().Send(context.Background(), srv.URL, "a", "t", []byte(`{}`)); err != nil {
		t.Fatalf("Send: 204 should count as success: %v", err)
	}
}

func TestSendErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), srv.URL, "a", "t", []byte(`{}`))
	if err == nil {
		t.Fatal("Send: want error for 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should carry the status: %v", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error should carry the body: %v", err)
	}
}

func TestSendConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()

	if err := New().Send(context.Background(), url, "a", "t", []byte(`{}`)); err == nil {
		t.Fatal("Send: want error for a closed server")
	}
}

func TestSendContextDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New().Send(ctx, srv.URL, "a", "t", []byte(`{}`)); err == nil {
		t.Fatal("Send: want error for a cancelled context")
	}
}
