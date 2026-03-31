package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePathTextBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/phash", strings.NewReader(`"/tmp/video.mp4"`))
	got, err := parsePath(r)
	if err != nil {
		t.Fatalf("parsePath returned error: %v", err)
	}
	if got != "/tmp/video.mp4" {
		t.Fatalf("unexpected path: got %q", got)
	}
}

func TestParsePathJSONBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/phash", strings.NewReader(`{"path":" /tmp/video.mp4 "}`))
	r.Header.Set("Content-Type", "application/json")

	got, err := parsePath(r)
	if err != nil {
		t.Fatalf("parsePath returned error: %v", err)
	}
	if got != "/tmp/video.mp4" {
		t.Fatalf("unexpected path: got %q", got)
	}
}

func TestParsePathInvalidJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/phash", strings.NewReader(`{"path":`))
	r.Header.Set("Content-Type", "application/json")

	if _, err := parsePath(r); err == nil {
		t.Fatal("expected parsePath to return JSON error")
	}
}
