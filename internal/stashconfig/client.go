package stashconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type gqlReq struct {
	Query string `json:"query"`
}

type gqlResp struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type statsPayload struct {
	SceneCount int `json:"scene_count"`
}

type findScenesPayload struct {
	Count int `json:"count"`
}

type versionPayload struct {
	Version string `json:"version"`
}

type systemStatusPayload struct {
	AppSchema string `json:"appSchema"`
}

type SceneCard struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Date           string      `json:"date,omitempty"`
	Details        string      `json:"details,omitempty"`
	StudioID       string      `json:"studioId,omitempty"`
	Studio         string      `json:"studio,omitempty"`
	Performers     []Performer `json:"performers,omitempty"`
	TagCount       int         `json:"tagCount"`
	PerformerCount int         `json:"performerCount"`
	GroupCount     int         `json:"groupCount"`
	OCount         int         `json:"oCount"`
	ResolutionX    int         `json:"resolutionX"`
	ResolutionY    int         `json:"resolutionY"`
	Duration       float64     `json:"duration"`
	MarkerCount    int         `json:"markerCount"`
	StashIDCount   int         `json:"stashIdCount"`
	FileCount      int         `json:"fileCount"`
}

type Performer struct {
	Name   string `json:"name"`
	Gender string `json:"gender,omitempty"`
}

func QueryVersion(ctx context.Context, client *http.Client, graphqlURL, apiKey string) (string, error) {
	queries := []string{
		`query { version { version } }`,
		`query { systemStatus { appSchema } }`,
	}

	var lastErr error
	for _, q := range queries {
		v, err := doQuery(ctx, client, graphqlURL, apiKey, q)
		if err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("empty version response")
	}
	return "", fmt.Errorf("failed to query stash endpoint: %w", lastErr)
}

func QuerySceneCounts(ctx context.Context, client *http.Client, graphqlURL, apiKey string) (withPhash int, total int, err error) {
	totalQ := `query { stats { scene_count } }`
	raw, err := doQueryRaw(ctx, client, graphqlURL, apiKey, totalQ)
	if err != nil {
		return 0, 0, err
	}
	statsRaw, ok := raw["stats"]
	if !ok {
		return 0, 0, fmt.Errorf("missing stats in response")
	}
	var sp statsPayload
	if err := json.Unmarshal(statsRaw, &sp); err != nil {
		return 0, 0, err
	}

	withPhashQ := `query { findScenes(scene_filter:{phash:{value:"",modifier:NOT_NULL}}){ count } }`
	raw, err = doQueryRaw(ctx, client, graphqlURL, apiKey, withPhashQ)
	if err != nil {
		return 0, 0, err
	}
	findRaw, ok := raw["findScenes"]
	if !ok {
		return 0, 0, fmt.Errorf("missing findScenes in response")
	}
	var fp findScenesPayload
	if err := json.Unmarshal(findRaw, &fp); err != nil {
		return 0, 0, err
	}

	return fp.Count, sp.SceneCount, nil
}

func QuerySceneCard(ctx context.Context, client *http.Client, graphqlURL, apiKey, sceneID string) (SceneCard, error) {
	sceneArg := fmt.Sprintf("%q", sceneID)
	if n, err := strconv.Atoi(strings.TrimSpace(sceneID)); err == nil && n > 0 {
		sceneArg = strconv.Itoa(n)
	}
	query := fmt.Sprintf(`query {
  findScene(id: %s) {
    id
    title
    date
    details
    studio { id name }
    performers { name gender }
    tags { id }
    groups { id }
    o_counter
    scene_markers { id }
    stash_ids { stash_id }
    files { id width height duration }
  }
}`, sceneArg)
	raw, err := doQueryRaw(ctx, client, graphqlURL, apiKey, query)
	if err != nil {
		return SceneCard{}, err
	}
	sceneRaw, ok := raw["findScene"]
	if !ok {
		return SceneCard{}, fmt.Errorf("missing findScene in response")
	}
	var payload struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Date    string `json:"date"`
		Details string `json:"details"`
		Studio  *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"studio"`
		Performers []Performer `json:"performers"`
		Tags       []struct {
			ID string `json:"id"`
		} `json:"tags"`
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
		OCounter     int `json:"o_counter"`
		SceneMarkers []struct {
			ID string `json:"id"`
		} `json:"scene_markers"`
		StashIDs []struct {
			StashID string `json:"stash_id"`
		} `json:"stash_ids"`
		Files []struct {
			ID       string  `json:"id"`
			Width    int     `json:"width"`
			Height   int     `json:"height"`
			Duration float64 `json:"duration"`
		} `json:"files"`
	}
	if err := json.Unmarshal(sceneRaw, &payload); err != nil {
		return SceneCard{}, err
	}
	card := SceneCard{
		ID:             payload.ID,
		Title:          payload.Title,
		Date:           payload.Date,
		Details:        payload.Details,
		Performers:     payload.Performers,
		TagCount:       len(payload.Tags),
		PerformerCount: len(payload.Performers),
		GroupCount:     len(payload.Groups),
		OCount:         payload.OCounter,
		MarkerCount:    len(payload.SceneMarkers),
		StashIDCount:   len(payload.StashIDs),
		FileCount:      len(payload.Files),
	}
	if payload.Studio != nil {
		card.StudioID = payload.Studio.ID
		card.Studio = payload.Studio.Name
	}
	if len(payload.Files) > 0 {
		card.ResolutionX = payload.Files[0].Width
		card.ResolutionY = payload.Files[0].Height
		card.Duration = payload.Files[0].Duration
	}
	return card, nil
}

func doQuery(ctx context.Context, client *http.Client, graphqlURL, apiKey, query string) (string, error) {
	out, err := doQueryRaw(ctx, client, graphqlURL, apiKey, query)
	if err != nil {
		return "", err
	}

	if raw, ok := out["version"]; ok {
		var vp versionPayload
		if err := json.Unmarshal(raw, &vp); err == nil && vp.Version != "" {
			return vp.Version, nil
		}
	}
	if raw, ok := out["systemStatus"]; ok {
		var sp systemStatusPayload
		if err := json.Unmarshal(raw, &sp); err == nil && sp.AppSchema != "" {
			return sp.AppSchema, nil
		}
	}
	return "", fmt.Errorf("unsupported response shape")
}

func doQueryRaw(ctx context.Context, client *http.Client, graphqlURL, apiKey, query string) (map[string]json.RawMessage, error) {
	body, _ := json.Marshal(gqlReq{Query: query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("ApiKey", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	var out gqlResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		return nil, errors.New(out.Errors[0].Message)
	}
	return out.Data, nil
}
