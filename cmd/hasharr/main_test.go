package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hasharr/internal/phash"
	"hasharr/internal/stashconfig"
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

func TestHandleFSList(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "Zfolder"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "afile.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/fs/list?path="+tmp, nil)
	rec := httptest.NewRecorder()
	handleFSList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var out fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out.Entries))
	}
	if !out.Entries[0].IsDir {
		t.Fatalf("expected directories first, got first entry: %+v", out.Entries[0])
	}
}

func TestHandleStashEndpointsRefresh(t *testing.T) {
	srv := newMockGraphQLServer(t)
	defer srv.Close()

	store := newStoreWithOneEndpoint(t, srv.URL)
	configStore = store

	req := httptest.NewRequest(http.MethodGet, "/v1/stash-endpoints?refresh=1", nil)
	rec := httptest.NewRecorder()
	handleStashEndpoints(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rec.Code, rec.Body.String())
	}

	var eps []stashconfig.Endpoint
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatalf("decode endpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(eps))
	}
	if eps[0].SceneCount != 5 || eps[0].TotalSceneCount != 10 {
		t.Fatalf("unexpected scene metrics: %+v", eps[0])
	}
	if eps[0].PhashPercent != 50 {
		t.Fatalf("expected 50%% phash coverage, got %v", eps[0].PhashPercent)
	}
}

func TestHandleStashEndpointByIDVersion(t *testing.T) {
	srv := newMockGraphQLServer(t)
	defer srv.Close()

	store := newStoreWithOneEndpoint(t, srv.URL)
	configStore = store

	req := httptest.NewRequest(http.MethodGet, "/v1/stash-endpoints/ep1/version", nil)
	rec := httptest.NewRecorder()
	handleStashEndpointByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if out["version"] != "v0.31.0" {
		t.Fatalf("unexpected version payload: %+v", out)
	}
}

func newStoreWithOneEndpoint(t *testing.T, graphqlURL string) *stashconfig.Store {
	t.Helper()
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "endpoints.json")
	initial := []stashconfig.Endpoint{
		{
			ID:              "ep1",
			Name:            "Primary Stash",
			GraphQLURL:      graphqlURL,
			APIKey:          "",
			Version:         "v0.0.0",
			SceneCount:      0,
			TotalSceneCount: 0,
			PhashPercent:    0,
			LastValidatedAt: time.Now().UTC(),
		},
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial: %v", err)
	}
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	store, err := stashconfig.NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func newMockGraphQLServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch {
		case strings.Contains(req.Query, "stats"):
			_, _ = w.Write([]byte(`{"data":{"stats":{"scene_count":10}}}`))
		case strings.Contains(req.Query, "findScenes"):
			_, _ = w.Write([]byte(`{"data":{"findScenes":{"count":5}}}`))
		case strings.Contains(req.Query, "version"):
			_, _ = w.Write([]byte(`{"data":{"version":{"version":"v0.31.0"}}}`))
		case strings.Contains(req.Query, "systemStatus"):
			_, _ = w.Write([]byte(`{"data":{"systemStatus":{"appSchema":"v0.31.0"}}}`))
		default:
			_, _ = w.Write([]byte(`{"errors":[{"message":"unsupported query"}]}`))
		}
	}))
}
