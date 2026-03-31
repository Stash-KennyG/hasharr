package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hasharr/internal/phash"
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

func TestHandlePHashSuccess(t *testing.T) {
	original := computePHash
	computePHash = func(_ context.Context, path string) (*phash.Result, error) {
		if path != "/tmp/video.mp4" {
			t.Fatalf("unexpected path passed to compute: %q", path)
		}
		return &phash.Result{
			PHash:       "0011223344556677",
			ResolutionX: 1280,
			ResolutionY: 720,
			Duration:    123.45,
			Bitrate:     1400.1,
		}, nil
	}
	t.Cleanup(func() { computePHash = original })

	req := httptest.NewRequest(http.MethodPost, "/v1/phash", strings.NewReader(`"/tmp/video.mp4"`))
	rec := httptest.NewRecorder()
	handlePHash(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rec.Code, rec.Body.String())
	}

	var got phash.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response json: %v", err)
	}
	if got.PHash != "0011223344556677" {
		t.Fatalf("unexpected phash: %s", got.PHash)
	}
}

func TestHandlePHashBadMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/phash", nil)
	rec := httptest.NewRecorder()
	handlePHash(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status: got %d", rec.Code)
	}
}

func TestHandlePHashMissingPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/phash", strings.NewReader("   "))
	rec := httptest.NewRecorder()
	handlePHash(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d", rec.Code)
	}
}

func TestHandlePHashComputeError(t *testing.T) {
	original := computePHash
	computePHash = func(_ context.Context, _ string) (*phash.Result, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { computePHash = original })

	req := httptest.NewRequest(http.MethodPost, "/v1/phash", strings.NewReader(`"/tmp/video.mp4"`))
	rec := httptest.NewRecorder()
	handlePHash(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d body=%s", rec.Code, rec.Body.String())
	}
}
