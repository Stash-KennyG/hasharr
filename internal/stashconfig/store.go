package stashconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Endpoint struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	GraphQLURL      string    `json:"graphqlUrl"`
	APIKey          string    `json:"apiKey,omitempty"`
	Version         string    `json:"version"`
	SceneCount      int       `json:"sceneCount"`
	TotalSceneCount int       `json:"totalSceneCount"`
	PhashPercent    float64   `json:"phashPercent"`
	LastValidatedAt time.Time `json:"lastValidatedAt"`
}

type Store struct {
	mu        sync.RWMutex
	filePath  string
	endpoints []Endpoint
}

func NewStore(filePath string) (*Store, error) {
	s := &Store{filePath: filePath}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) List() []Endpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Endpoint, len(s.endpoints))
	copy(out, s.endpoints)
	return out
}

func (s *Store) Create(ctx context.Context, req Endpoint, client *http.Client) (Endpoint, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.GraphQLURL = strings.TrimSpace(req.GraphQLURL)
	if req.Name == "" || req.GraphQLURL == "" {
		return Endpoint{}, fmt.Errorf("name and graphqlUrl are required")
	}

	version, err := QueryVersion(ctx, client, req.GraphQLURL, req.APIKey)
	if err != nil {
		return Endpoint{}, err
	}
	withPhash, totalScenes, err := QuerySceneCounts(ctx, client, req.GraphQLURL, req.APIKey)
	if err != nil {
		return Endpoint{}, err
	}
	percent := 0.0
	if totalScenes > 0 {
		percent = (float64(withPhash) * 100.0) / float64(totalScenes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e := Endpoint{
		ID:              fmt.Sprintf("%d", time.Now().UnixNano()),
		Name:            req.Name,
		GraphQLURL:      req.GraphQLURL,
		APIKey:          req.APIKey,
		Version:         version,
		SceneCount:      withPhash,
		TotalSceneCount: totalScenes,
		PhashPercent:    percent,
		LastValidatedAt: time.Now().UTC(),
	}
	s.endpoints = append(s.endpoints, e)
	return e, s.saveLocked()
}

func (s *Store) Update(ctx context.Context, id string, req Endpoint, client *http.Client) (Endpoint, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.GraphQLURL = strings.TrimSpace(req.GraphQLURL)
	if req.Name == "" || req.GraphQLURL == "" {
		return Endpoint{}, fmt.Errorf("name and graphqlUrl are required")
	}

	version, err := QueryVersion(ctx, client, req.GraphQLURL, req.APIKey)
	if err != nil {
		return Endpoint{}, err
	}
	withPhash, totalScenes, err := QuerySceneCounts(ctx, client, req.GraphQLURL, req.APIKey)
	if err != nil {
		return Endpoint{}, err
	}
	percent := 0.0
	if totalScenes > 0 {
		percent = (float64(withPhash) * 100.0) / float64(totalScenes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.endpoints {
		if s.endpoints[i].ID == id {
			s.endpoints[i].Name = req.Name
			s.endpoints[i].GraphQLURL = req.GraphQLURL
			s.endpoints[i].APIKey = req.APIKey
			s.endpoints[i].Version = version
			s.endpoints[i].SceneCount = withPhash
			s.endpoints[i].TotalSceneCount = totalScenes
			s.endpoints[i].PhashPercent = percent
			s.endpoints[i].LastValidatedAt = time.Now().UTC()
			return s.endpoints[i], s.saveLocked()
		}
	}
	return Endpoint{}, fmt.Errorf("endpoint not found")
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.endpoints {
		if s.endpoints[i].ID == id {
			s.endpoints = append(s.endpoints[:i], s.endpoints[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("endpoint not found")
}

func (s *Store) RefreshMetricsAll(ctx context.Context, client *http.Client) ([]Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.endpoints {
		withPhash, totalScenes, err := QuerySceneCounts(ctx, client, s.endpoints[i].GraphQLURL, s.endpoints[i].APIKey)
		if err != nil {
			return nil, fmt.Errorf("refresh metrics for %s: %w", s.endpoints[i].Name, err)
		}
		percent := 0.0
		if totalScenes > 0 {
			percent = (float64(withPhash) * 100.0) / float64(totalScenes)
		}
		s.endpoints[i].SceneCount = withPhash
		s.endpoints[i].TotalSceneCount = totalScenes
		s.endpoints[i].PhashPercent = percent
		s.endpoints[i].LastValidatedAt = time.Now().UTC()
	}

	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	out := make([]Endpoint, len(s.endpoints))
	copy(out, s.endpoints)
	return out, nil
}

func (s *Store) RefreshVersion(ctx context.Context, id string, client *http.Client) (Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.endpoints {
		if s.endpoints[i].ID != id {
			continue
		}
		version, err := QueryVersion(ctx, client, s.endpoints[i].GraphQLURL, s.endpoints[i].APIKey)
		if err != nil {
			return Endpoint{}, err
		}
		s.endpoints[i].Version = version
		s.endpoints[i].LastValidatedAt = time.Now().UTC()
		if err := s.saveLocked(); err != nil {
			return Endpoint{}, err
		}
		return s.endpoints[i], nil
	}
	return Endpoint{}, fmt.Errorf("endpoint not found")
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.endpoints = []Endpoint{}
			return nil
		}
		return err
	}
	if len(data) == 0 {
		s.endpoints = []Endpoint{}
		return nil
	}
	return json.Unmarshal(data, &s.endpoints)
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.endpoints, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0o600)
}
