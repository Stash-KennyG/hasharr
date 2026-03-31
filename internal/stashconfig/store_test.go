package stashconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadBackfillsPublicURL(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "endpoints.json")
	initial := []Endpoint{
		{
			ID:              "ep1",
			Name:            "Primary",
			GraphQLURL:      "http://stash:9999/graphql",
			PublicURL:       "",
			Version:         "v0.31.0",
			SceneCount:      1,
			TotalSceneCount: 2,
			PhashPercent:    50,
			LastValidatedAt: time.Now().UTC(),
		},
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial: %v", err)
	}
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	got := store.List()
	if len(got) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(got))
	}
	if got[0].PublicURL != got[0].GraphQLURL {
		t.Fatalf("expected publicUrl fallback to graphqlUrl, got public=%q graphql=%q", got[0].PublicURL, got[0].GraphQLURL)
	}
}

func TestCreateDefaultsPublicURL(t *testing.T) {
	srv := newStoreMockGraphQLServer(t)
	defer srv.Close()
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "endpoints.json")
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ep, err := store.Create(context.Background(), Endpoint{
		Name:       "Primary",
		GraphQLURL: srv.URL,
		APIKey:     "",
	}, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ep.PublicURL != ep.GraphQLURL {
		t.Fatalf("expected publicUrl fallback to graphqlUrl, got public=%q graphql=%q", ep.PublicURL, ep.GraphQLURL)
	}
}

func TestUpdateDefaultsPublicURL(t *testing.T) {
	srv := newStoreMockGraphQLServer(t)
	defer srv.Close()
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "endpoints.json")
	initial := []Endpoint{
		{
			ID:              "ep1",
			Name:            "Primary",
			GraphQLURL:      srv.URL,
			PublicURL:       "https://public.old/graphql",
			Version:         "v0.31.0",
			SceneCount:      1,
			TotalSceneCount: 2,
			PhashPercent:    50,
			LastValidatedAt: time.Now().UTC(),
		},
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial: %v", err)
	}
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ep, err := store.Update(context.Background(), "ep1", Endpoint{
		Name:       "Primary",
		GraphQLURL: srv.URL,
		PublicURL:  "",
		APIKey:     "",
	}, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if ep.PublicURL != ep.GraphQLURL {
		t.Fatalf("expected publicUrl fallback to graphqlUrl, got public=%q graphql=%q", ep.PublicURL, ep.GraphQLURL)
	}
}

func newStoreMockGraphQLServer(t *testing.T) *httptest.Server {
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
