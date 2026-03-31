package stashconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type versionPayload struct {
	Version string `json:"version"`
}

type systemStatusPayload struct {
	AppSchema string `json:"appSchema"`
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

func doQuery(ctx context.Context, client *http.Client, graphqlURL, apiKey, query string) (string, error) {
	body, _ := json.Marshal(gqlReq{Query: query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("ApiKey", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}

	var out gqlResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Errors) > 0 {
		return "", errors.New(out.Errors[0].Message)
	}

	if raw, ok := out.Data["version"]; ok {
		var vp versionPayload
		if err := json.Unmarshal(raw, &vp); err == nil && vp.Version != "" {
			return vp.Version, nil
		}
	}
	if raw, ok := out.Data["systemStatus"]; ok {
		var sp systemStatusPayload
		if err := json.Unmarshal(raw, &sp); err == nil && sp.AppSchema != "" {
			return sp.AppSchema, nil
		}
	}
	return "", fmt.Errorf("unsupported response shape")
}
