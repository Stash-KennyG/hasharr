package stashconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"net/http"
	"strconv"
)

type SceneMatch struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Path         string  `json:"path"`
	Duration     float64 `json:"duration"`
	Distance     int     `json:"distance"`
	DurationDiff float64 `json:"durationDiff"`
}

type SceneLookupResult struct {
	ExactMatches     []SceneMatch `json:"exactMatches"`
	PartialMatches   []SceneMatch `json:"partialMatches"`
	DurationWindowS  float64      `json:"durationWindowSeconds"`
	PartialDistance  int          `json:"partialDistanceMax"`
	LookupSkippedMsg string       `json:"lookupSkipped,omitempty"`
}

type sceneLookupRawResp struct {
	Data struct {
		FindScenes struct {
			Scenes []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Files []struct {
					Path         string  `json:"path"`
					Duration     float64 `json:"duration"`
					Fingerprints []struct {
						Type  string `json:"type"`
						Value string `json:"value"`
					} `json:"fingerprints"`
				} `json:"files"`
			} `json:"scenes"`
		} `json:"findScenes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func LookupSceneMatches(ctx context.Context, client *http.Client, graphqlURL, apiKey, phashHex string, duration float64) (SceneLookupResult, error) {
	return LookupSceneMatchesWithOptions(ctx, client, graphqlURL, apiKey, phashHex, duration, 1, 0)
}

func LookupSceneMatchesWithOptions(ctx context.Context, client *http.Client, graphqlURL, apiKey, phashHex string, duration float64, maxTimeDelta float64, maxDistance int) (SceneLookupResult, error) {
	if maxTimeDelta < 0 {
		maxTimeDelta = 0
	}
	if maxTimeDelta > 15 {
		maxTimeDelta = 15
	}
	if maxDistance < 0 {
		maxDistance = 0
	}
	if maxDistance > 8 {
		maxDistance = 8
	}

	matches, err := querySceneMatches(ctx, client, graphqlURL, apiKey, phashHex, maxDistance, duration, maxTimeDelta)
	if err != nil {
		return SceneLookupResult{}, err
	}

	res := SceneLookupResult{
		DurationWindowS: maxTimeDelta,
		PartialDistance: maxDistance,
	}
	for _, m := range matches {
		if m.Distance == 0 {
			res.ExactMatches = append(res.ExactMatches, m)
		} else {
			res.PartialMatches = append(res.PartialMatches, m)
		}
	}
	return res, nil
}

func querySceneMatches(ctx context.Context, client *http.Client, graphqlURL, apiKey, phashHex string, maxDistance int, duration, window float64) ([]SceneMatch, error) {
	minDur := int(math.Floor(duration - window))
	if minDur < 0 {
		minDur = 0
	}
	maxDur := int(math.Ceil(duration + window))

	query := `query FindSceneMatches($sf: SceneFilterType, $f: FindFilterType) {
	  findScenes(scene_filter: $sf, filter: $f) {
	    scenes {
	      id
	      title
	      files {
	        path
	        duration
	        fingerprints { type value }
	      }
	    }
	  }
	}`

	variables := map[string]any{
		"sf": map[string]any{
			"phash_distance": map[string]any{
				"value":    phashHex,
				"modifier": "EQUALS",
				"distance": maxDistance + 1, // sqlite handler uses "< distance"
			},
			"duration": map[string]any{
				"value":    minDur,
				"value2":   maxDur,
				"modifier": "BETWEEN",
			},
		},
		"f": map[string]any{
			"per_page": 50,
		},
	}

	reqBody, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
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

	var out sceneLookupRawResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		return nil, errors.New(out.Errors[0].Message)
	}

	matches := make([]SceneMatch, 0, len(out.Data.FindScenes.Scenes))
	targetVal, _ := strconv.ParseUint(phashHex, 16, 64)
	for _, sc := range out.Data.FindScenes.Scenes {
		if len(sc.Files) == 0 {
			continue
		}
		f := sc.Files[0]
		dist := 0
		for _, fp := range f.Fingerprints {
			if fp.Type == "phash" {
				if v, err := strconv.ParseUint(fp.Value, 10, 64); err == nil {
					dist = bits.OnesCount64(targetVal ^ v)
				}
				break
			}
		}
		matches = append(matches, SceneMatch{
			ID:           sc.ID,
			Title:        sc.Title,
			Path:         f.Path,
			Duration:     f.Duration,
			Distance:     dist,
			DurationDiff: math.Abs(f.Duration - duration),
		})
	}

	return matches, nil
}
