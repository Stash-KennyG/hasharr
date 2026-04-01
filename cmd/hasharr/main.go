package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"hasharr/internal/phash"
	"hasharr/internal/recordstats"
	"hasharr/internal/stashconfig"
)

type requestBody struct {
	Path       string `json:"path"`
	EndpointID string `json:"endpointId,omitempty"`
}

type phashMatchRequest struct {
	FilePath     string   `json:"filePath"`
	StashIndex   *int     `json:"stashIndex,omitempty"`
	MaxTimeDelta *float64 `json:"maxTimeDelta,omitempty"`
	MaxDistance  *int     `json:"maxDistance,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type fsEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

type fsListResponse struct {
	Path    string    `json:"path"`
	Entries []fsEntry `json:"entries"`
}

type sceneCardRequest struct {
	EndpointURL string `json:"endpointUrl"`
	SceneID     string `json:"sceneId"`
}

type recordStatsRequest struct {
	SABNzoID            string  `json:"sabNzoID"`
	FileName            string  `json:"fileName"`
	FileSizeBytes       int64   `json:"fileSizeBytes"`
	FileDurationSeconds float64 `json:"fileDurationSeconds"`
	HashDurationSeconds float64 `json:"hashDurationSeconds"`
	Outcome             int     `json:"outcome"`
}

var computePHash = phash.Compute
var configStore *stashconfig.Store
var statsStore *recordstats.Store
var resourcesDir string
var lookupMatches = stashconfig.LookupSceneMatchesWithOptions
var buildID = "local"

//go:embed VERSION
var versionSeriesRaw string

func main() {
	addr := envOrDefault("HASHARR_ADDR", ":9995")
	configPath := envOrDefault("HASHARR_CONFIG_FILE", "/config/config.json")
	resourcesDir = envOrDefault("HASHARR_RESOURCES_DIR", "./resources")
	store, err := stashconfig.NewStore(configPath)
	if err != nil {
		log.Fatal(err)
	}
	configStore = store
	statsPath := filepath.Join(filepath.Dir(configPath), "hasharr-stats.db")
	sStore, err := recordstats.New(statsPath)
	if err != nil {
		log.Fatal(err)
	}
	statsStore = sStore

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.HandleFunc("/favicon-source.png", handleFaviconSource)
	mux.HandleFunc("/logo.png", handleLogo)
	mux.HandleFunc("/app.css", handleAppCSS)
	mux.HandleFunc("/v1/sab-postprocess.py", handleSABPostProcessScript)
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/v1/phash", handlePHash)
	mux.HandleFunc("/v1/phash-match", handlePHashMatch)
	mux.HandleFunc("/v1/scene-card", handleSceneCard)
	mux.HandleFunc("/v1/fs/list", handleFSList)
	mux.HandleFunc("/v1/stash-endpoints", handleStashEndpoints)
	mux.HandleFunc("/v1/stash-endpoints/", handleStashEndpointByID)
	mux.HandleFunc("/v1/stash-endpoints-test", handleStashEndpointTest)
	mux.HandleFunc("/v1/record-stats", handleRecordStats)
	mux.HandleFunc("/v1/stats-summary", handleRecordStatsSummary)

	server := &http.Server{
		Addr:              addr,
		Handler:           withLogging(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("hasharr listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	versionSeries := strings.TrimSpace(versionSeriesRaw)
	if versionSeries == "" {
		versionSeries = "0.0"
	}
	build := strings.TrimSpace(buildID)
	if build == "" {
		build = "local"
	}
	versionText := "hasharr v." + versionSeries + "." + build
	versionTip := ""
	if strings.EqualFold(build, "local") {
		versionTip = "running from local resources, outside standard build methods."
	}
	html := strings.ReplaceAll(configPageHTML, "__HASHARR_VERSION__", versionText)
	html = strings.ReplaceAll(html, "__HASHARR_VERSION_TOOLTIP__", versionTip)
	_, _ = io.WriteString(w, html)
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	faviconPath := resourcesDir + "/favicon.ico"
	if _, err := os.Stat(faviconPath); err == nil {
		http.ServeFile(w, r, faviconPath)
		return
	}
	// Local/dev fallback when favicon is not pre-generated.
	http.ServeFile(w, r, resourcesDir+"/favicon_source.png")
}

func handleFaviconSource(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, resourcesDir+"/favicon_source.png")
}

func handleLogo(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, resourcesDir+"/logo.png")
}

func handleAppCSS(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, resourcesDir+"/app.css")
}

func handleSABPostProcessScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	stashIndex := clampIntQuery(r.URL.Query().Get("stashIndex"), -1, -1, 99999)
	maxTimeDelta := clampFloatQuery(r.URL.Query().Get("maxTimeDelta"), 1, 0, 15)
	maxDistance := clampIntQuery(r.URL.Query().Get("maxDistance"), 0, 0, 8)
	hasharrURL := strings.TrimSpace(r.URL.Query().Get("hasharrUrl"))
	if hasharrURL == "" {
		hasharrURL = strings.TrimSpace(r.Header.Get("Origin"))
	}
	if hasharrURL == "" {
		hasharrURL = "http://hasharr:9995"
	}

	scriptPath := filepath.Join(resourcesDir, "sab_postProcess.py")
	b, err := os.ReadFile(scriptPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to read script template")
		return
	}
	src := string(b)
	cfg := "\n# Download-time defaults from hasharr configurator UI.\n" +
		"DEFAULT_STASH_INDEX = " + strconv.Itoa(stashIndex) + "\n" +
		"DEFAULT_MAX_TIME_DELTA = " + strconv.FormatFloat(maxTimeDelta, 'f', 3, 64) + "\n" +
		"DEFAULT_MAX_DISTANCE = " + strconv.Itoa(maxDistance) + "\n" +
		"DEFAULT_HASHARR_URL = " + strconv.Quote(hasharrURL) + "\n\n"

	out := injectPythonDefaults(src, cfg)

	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sab_postProcess.py"`)
	_, _ = io.WriteString(w, out)
}

func injectPythonDefaults(src, cfg string) string {
	// Keep `from __future__` imports as the first statement after docstring/comments.
	futureLine := "from __future__ import "
	if i := strings.Index(src, futureLine); i >= 0 {
		if j := strings.Index(src[i:], "\n"); j >= 0 {
			k := i + j + 1
			return src[:k] + cfg + src[k:]
		}
		return src + "\n" + cfg
	}

	// Fallback: insert after shebang when present.
	if strings.HasPrefix(src, "#!") {
		if i := strings.Index(src, "\n"); i >= 0 {
			return src[:i+1] + cfg + src[i+1:]
		}
	}
	return cfg + src
}

func clampIntQuery(raw string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func clampFloatQuery(raw string, fallback, min, max float64) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	if f < min {
		return min
	}
	if f > max {
		return max
	}
	return f
}

func handleRecordStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req recordStatsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.SABNzoID = strings.TrimSpace(req.SABNzoID)
	req.FileName = strings.TrimSpace(req.FileName)
	if req.FileName == "" {
		writeErr(w, http.StatusBadRequest, "fileName is required")
		return
	}
	if req.FileSizeBytes < 0 {
		writeErr(w, http.StatusBadRequest, "fileSizeBytes must be >= 0")
		return
	}
	if req.FileDurationSeconds < 0 {
		writeErr(w, http.StatusBadRequest, "fileDurationSeconds must be >= 0")
		return
	}
	if req.HashDurationSeconds < 0 {
		writeErr(w, http.StatusBadRequest, "hashDurationSeconds must be >= 0")
		return
	}
	if req.Outcome < 0 || req.Outcome > 15 {
		writeErr(w, http.StatusBadRequest, "outcome must be between 0 and 15")
		return
	}
	if err := statsStore.Insert(r.Context(), recordstats.Record{
		SABNzoID:            req.SABNzoID,
		FileName:            req.FileName,
		FileSizeBytes:       req.FileSizeBytes,
		FileDurationSeconds: req.FileDurationSeconds,
		HashDurationSeconds: req.HashDurationSeconds,
		Outcome:             req.Outcome,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleRecordStatsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s, err := statsStore.Summary(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handlePHash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	videoPath, err := parsePath(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if videoPath == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	result, err := computePHash(ctx, videoPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func handlePHashMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req phashMatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		writeErr(w, http.StatusBadRequest, "filePath is required")
		return
	}
	stashIndex := -1
	if req.StashIndex != nil {
		stashIndex = *req.StashIndex
	}
	maxTimeDelta := 1.0
	if req.MaxTimeDelta != nil {
		maxTimeDelta = *req.MaxTimeDelta
	}
	if maxTimeDelta < 0 {
		maxTimeDelta = 0
	}
	if maxTimeDelta > 15 {
		maxTimeDelta = 15
	}
	maxDistance := 0
	if req.MaxDistance != nil {
		maxDistance = *req.MaxDistance
	}
	if maxDistance < 0 {
		maxDistance = 0
	}
	if maxDistance > 8 {
		maxDistance = 8
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	hashResult, err := computePHash(ctx, req.FilePath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	endpoints := configStore.List()
	type endpointLookup struct {
		EndpointURL  string                        `json:"endpointUrl"`
		PublicURL    string                        `json:"publicUrl"`
		EndpointName string                        `json:"endpointName"`
		Matches      stashconfig.SceneLookupResult `json:"matches"`
	}
	lookups := []endpointLookup{}

	selected := endpoints
	if stashIndex >= 0 {
		if stashIndex >= len(endpoints) {
			writeErr(w, http.StatusBadRequest, "stashIndex out of range")
			return
		}
		selected = []stashconfig.Endpoint{endpoints[stashIndex]}
	}

	if len(selected) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"hash":          hashResult,
			"lookups":       lookups,
			"stashIndex":    stashIndex,
			"maxTimeDelta":  maxTimeDelta,
			"maxDistance":   maxDistance,
			"lookupSkipped": "no endpoints configured",
		})
		return
	}

	for _, ep := range selected {
		lctx, lcancel := context.WithTimeout(r.Context(), 40*time.Second)
		lookup, err := lookupMatches(
			lctx,
			&http.Client{Timeout: 20 * time.Second},
			ep.GraphQLURL,
			ep.APIKey,
			hashResult.PHash,
			hashResult.Duration,
			maxTimeDelta,
			maxDistance,
		)
		lcancel()
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		lookups = append(lookups, endpointLookup{
			EndpointURL:  ep.GraphQLURL,
			PublicURL:    ep.PublicURL,
			EndpointName: ep.Name,
			Matches:      lookup,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hash":         hashResult,
		"lookups":      lookups,
		"stashIndex":   stashIndex,
		"maxTimeDelta": maxTimeDelta,
		"maxDistance":  maxDistance,
	})
}

func handleFSList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	if requested == "" {
		if st, err := os.Stat("/downloads"); err == nil && st.IsDir() {
			requested = "/downloads"
		} else {
			requested = "/"
		}
	}

	abs, err := filepath.Abs(requested)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	target := abs
	if !info.IsDir() {
		target = filepath.Dir(abs)
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	out := make([]fsEntry, 0, len(entries))
	for _, ent := range entries {
		entInfo, err := ent.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(target, ent.Name())
		out = append(out, fsEntry{
			Name:     ent.Name(),
			Path:     fullPath,
			IsDir:    ent.IsDir(),
			Size:     entInfo.Size(),
			Modified: entInfo.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	writeJSON(w, http.StatusOK, fsListResponse{
		Path:    target,
		Entries: out,
	})
}

func handleSceneCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req sceneCardRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.EndpointURL = strings.TrimSpace(req.EndpointURL)
	req.SceneID = strings.TrimSpace(req.SceneID)
	if req.EndpointURL == "" || req.SceneID == "" {
		writeErr(w, http.StatusBadRequest, "endpointUrl and sceneId are required")
		return
	}
	var ep *stashconfig.Endpoint
	for _, candidate := range configStore.List() {
		if strings.EqualFold(strings.TrimSpace(candidate.GraphQLURL), req.EndpointURL) {
			c := candidate
			ep = &c
			break
		}
	}
	if ep == nil {
		writeErr(w, http.StatusBadRequest, "endpoint not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	card, err := stashconfig.QuerySceneCard(ctx, &http.Client{Timeout: 15 * time.Second}, ep.GraphQLURL, ep.APIKey, req.SceneID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func parsePath(r *http.Request) (string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return "", nil
	}

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var req requestBody
		if err := json.Unmarshal(body, &req); err != nil {
			return "", err
		}
		return strings.TrimSpace(req.Path), nil
	}

	// Allow plain body: "/path/to/video.mp4" or /path/to/video.mp4
	return strings.Trim(strings.TrimSpace(raw), `"`), nil
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errorResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func handleStashEndpoints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("refresh") == "1" {
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			out, err := configStore.RefreshMetricsAll(ctx, &http.Client{Timeout: 20 * time.Second})
			if err != nil {
				writeErr(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		writeJSON(w, http.StatusOK, configStore.List())
	case http.MethodPost:
		var req stashconfig.Endpoint
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json body")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		ep, err := configStore.Create(ctx, req, &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, ep)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleStashEndpointTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req stashconfig.Endpoint
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.GraphQLURL = strings.TrimSpace(req.GraphQLURL)
	if req.Name == "" || req.GraphQLURL == "" {
		writeErr(w, http.StatusBadRequest, "name and graphqlUrl are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	version, err := stashconfig.QueryVersion(ctx, &http.Client{Timeout: 15 * time.Second}, req.GraphQLURL, req.APIKey)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"name":    req.Name,
		"version": version,
	})
}

func handleStashEndpointByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/stash-endpoints/")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing endpoint id")
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing endpoint id")
		return
	}

	// Lazy version refresh endpoint: GET /v1/stash-endpoints/{id}/version
	if len(parts) == 2 && parts[1] == "version" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		ep, err := configStore.RefreshVersion(ctx, id, &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      ep.ID,
			"name":    ep.Name,
			"version": ep.Version,
		})
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req stashconfig.Endpoint
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json body")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		ep, err := configStore.Update(ctx, id, req, &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ep)
	case http.MethodDelete:
		if err := configStore.Delete(id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func envOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

var configPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>hasharr</title>
  <link rel="icon" href="/favicon.ico" sizes="32x32" />
  <link rel="stylesheet" href="/app.css" />
</head>
<body>
  <div class="container">
    <div class="brand">
      <img src="/logo.png" alt="hasharr logo" />
      <h1>hasharr</h1>
    </div>

    <section class="stats-ribbon">
      <table class="stats-table">
        <tr>
          <td title="Total number of per-file hash stats records."><div class="sub">Hashes</div><h2 id="statHashCount">0</h2></td>
          <td title="Sum of processed source file sizes in record-stats."><div class="sub">Data Hashed</div><h2 id="statDataSum">0 B</h2></td>
          <td title="Count of files deleted by exact-match quality logic."><div class="sub">Deletes</div><h2 id="statDeleteCount">0</h2></td>
          <td title="Count of files tagged with L (larger resolution)."><div class="sub">L Tags</div><h2 id="statLCount">0</h2></td>
          <td title="Count of files tagged with F (higher fps)."><div class="sub">F Tags</div><h2 id="statFCount">0</h2></td>
          <td title="Count of files tagged with D (longer duration)."><div class="sub">D Tags</div><h2 id="statDCount">0</h2></td>
          <td title="Sum of source video durations from record-stats."><div class="sub">Video Duration</div><h2 id="statVideoSum">0s</h2></td>
          <td title="Sum of hash processing elapsed time from record-stats."><div class="sub">Hash Time</div><h2 id="statHashTimeSum">0s</h2></td>
          <td title="Earliest timestamp in the stats table."><div class="sub">Since</div><h2 id="statSince">-</h2></td>
        </tr>
      </table>
    </section>

    <section class="panel collapsed" id="aboutDrawer">
      <div class="drawer-head" id="aboutDrawerToggle">
        <div class="drawer-title">📖 About</div>
        <div class="carrot">▾</div>
      </div>
      <div class="drawer-body single">
        <div class="sub">purpose and getting started</div>
        <div>hasharr is designed to sit in front of manual curation work. It hashes completed downloads, checks for perceptual matches in Stash, and helps remove or tag likely duplicates before you spend time organizing content by hand.</div>
        <div style="margin-top:8px;"><strong>Quick start</strong></div>
        <div>1) Open <strong>⚙️ Settings</strong> and add one or more Stash GraphQL endpoints.</div>
        <div>2) Open <strong>🐍 Configurator</strong> and set match behavior (`Stash Endpoints`, `maxTimeDelta`, `maxDistance`).</div>
        <div>3) Click <strong>Download Script</strong>, set the endpoint URL for your SAB environment, and save `sab_postProcess.py` into SABnzbd's scripts path.</div>
        <div>4) In SABnzbd, set that script as the post-process script for jobs/categories you want filtered.</div>
        <div>5) Use <strong>🏗 Playground</strong> to test against local files and validate matching behavior before relying on full automation.</div>
        <div style="margin-top:8px;">See the repository README for full Docker examples, API details, and SAB integration notes.</div>
      </div>
    </section>

    <section class="panel" id="settingsDrawer">
      <div class="drawer-head" id="drawerToggle">
        <div>
          <div class="drawer-title">⚙️ Settings</div>
          <div class="sub">stash endpoints and diagnostics</div>
        </div>
        <div class="carrot">▾</div>
      </div>
      <div class="drawer-body">
        <div class="card">
          <h3>Stash Endpoints</h3>
          <div class="sub">Configured instances</div>
          <ul id="list"></ul>
        </div>
        <div class="card">
          <h3 id="formTitle">Add Endpoint</h3>
          <div class="sub">Validated on save</div>
          <label>Name</label>
          <input id="name" placeholder="PrimaryStash" />
          <label>GraphQL Url</label>
          <input id="graphqlUrl" placeholder="http://stash.local:9999/graphql" />
          <label>Public Url (optional)</label>
          <input id="publicUrl" placeholder="https://stash.example.com/graphql" />
          <label>Api Key (optional)</label>
          <input id="apiKey" placeholder="ApiKey..." />
          <div class="row">
            <button class="grow" id="testBtn">Test</button>
            <button class="primary grow" id="saveBtn">Save</button>
            <button class="grow" id="newBtn">New</button>
            <button class="grow" id="deleteBtn">Delete</button>
          </div>
          <div class="status" id="status"></div>
          <div class="usage">
            <div><strong>Quick usage</strong></div>
            <div>- Add endpoint details, click <strong>Test</strong>, then <strong>Save</strong>.</div>
            <div>- Hover endpoint name for lazy version refresh.</div>
          </div>
        </div>
      </div>
    </section>

    <section class="workflow">
      <div class="panel" id="configDrawer">
        <div class="drawer-head" id="configDrawerToggle">
          <div class="drawer-title">🐍 Configurator</div>
          <div class="carrot">▾</div>
        </div>
        <div class="drawer-body single">
          <div class="pathbar">
            <div class="sub">phash-match configurator <span title="Defaults: stashIndex=-1 (All endpoints), maxTimeDelta=1s, maxDistance=0">ⓘ</span></div>
            <div class="pathrow">
              <label style="margin:0;min-width:122px;">Stash Endpoints:</label>
              <select id="stashIndex" style="width:280px; padding:9px; border:1px solid var(--border); border-radius:8px; background:#12161d; color:var(--text);">
                <option value="-1">All</option>
              </select>
              <label style="margin:0;min-width:95px;">maxTimeDelta</label>
              <input id="maxTimeDelta" type="number" min="0" max="15" step="1" value="1" style="width:90px;" />
              <label style="margin:0;min-width:88px;">maxDistance</label>
              <input id="maxDistance" type="range" min="0" max="8" step="1" value="0" style="width:130px;" />
              <span id="maxDistanceLabel" style="min-width:14px; text-align:right;">0</span>
              <button class="primary" id="downloadSabBtn" style="margin-top:0; margin-left:auto;">Download Script</button>
            </div>
          </div>
        </div>
      </div>
      <div class="panel" id="playgroundDrawer">
        <div class="drawer-head" id="playgroundDrawerToggle">
          <div class="drawer-title">🏗 Playground</div>
          <div class="carrot">▾</div>
        </div>
        <div class="drawer-body single">
          <div class="curlbar">
            <div class="sub">generated curl command</div>
            <div class="mono" id="curlCmd">Select a file to generate curl command.</div>
          </div>
          <div class="pathbar">
            <div class="sub">path</div>
            <div class="pathrow">
              <button id="upBtn">Up</button>
              <input id="pathInput" />
              <button class="primary" id="hashBtn">Hash</button>
            </div>
          </div>
          <div class="browser-results">
            <div class="card">
              <table>
                <thead><tr><th id="nameSortHead" title="Sort by name">Name</th><th id="sizeSortHead" title="Sort by size">Size</th><th id="modifiedSortHead" title="Sort by modified date">Date Modified</th></tr></thead>
                <tbody id="fileRows"></tbody>
              </table>
            </div>
            <div class="results">
              <div class="sub">results</div>
              <div class="spinner" id="spinner"></div>
              <div id="cards" class="cards"></div>
              <div id="rawDrawer" class="collapsed">
                <div class="raw-drawer-head" id="rawToggle"><span>Raw JSON</span><span class="raw-caret">▾</span></div>
                <div class="raw-body"><pre id="resultJson">Results</pre></div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
    <div id="sabModal" class="modal-backdrop hidden" role="dialog" aria-modal="true" aria-labelledby="sabModalTitle">
      <div class="modal-card">
        <h3 id="sabModalTitle">Download SAB Post-Process Script</h3>
        <div class="sub" style="text-transform:none; letter-spacing:normal;">
          Please specify endpoint URL. If running it in a container it is suggested to use your container name and leave it as http://hasharr:9995.
        </div>
        <label for="sabModalEndpoint">Endpoint URL</label>
        <input id="sabModalEndpoint" type="text" placeholder="https://hasharr:9995" />
        <div class="row modal-actions">
          <button id="sabModalCancelBtn" class="grow">Cancel</button>
          <button id="sabModalDetectBtn" class="grow">Detect URL</button>
          <button id="sabModalDownloadBtn" class="primary grow">Download</button>
        </div>
      </div>
    </div>
    <div class="sub footer-version" title="__HASHARR_VERSION_TOOLTIP__">__HASHARR_VERSION__</div>
  </div>

  <script>
    let endpoints = [];
    const versionLoaded = new Set();
    let selectedId = null;
    let currentPath = '';
    let selectedEntry = null;
    let entries = [];
    let sortKey = 'name';
    let sortAsc = true;
    let aboutDrawerInit = false;
    const el = (id) => document.getElementById(id);
    const status = (msg, cls='') => { el('status').className = 'status ' + cls; el('status').textContent = msg; };
    const showSpin = (on) => el('spinner').classList.toggle('show', !!on);
    const prettyVersion = (v) => { const s=String(v||'').trim(); return !s ? '' : (s[0]==='v'||s[0]==='V'?s:('v'+s)); };
    const fmtCount = (n) => {
      const v = Number(n || 0);
      if (!Number.isFinite(v)) return '0';
      const abs = Math.abs(v);
      const units = [
        { s: 1e12, u: 'T' },
        { s: 1e9, u: 'B' },
        { s: 1e6, u: 'M' },
        { s: 1e3, u: 'K' },
      ];
      for (const item of units) {
        if (abs >= item.s) {
          const scaled = v / item.s;
          return scaled.toFixed(1).replace(/\.0$/, '') + item.u;
        }
      }
      return Math.round(v).toLocaleString();
    };
    const versionTitle = (ep) => 'Version: ' + prettyVersion(ep.version);
    const countTitle = (ep) => Number(ep.phashPercent||0).toFixed(2) + '% phashes.  ' + fmtCount(ep.sceneCount) + ' of ' + fmtCount(ep.totalSceneCount) + ' scenes';

    function clearForm(){ el('name').value=''; el('graphqlUrl').value=''; el('publicUrl').value=''; el('apiKey').value=''; selectedId=null; el('formTitle').textContent='Add Endpoint'; renderList(); }
    function fillForm(ep){ el('name').value=ep.name; el('graphqlUrl').value=ep.graphqlUrl; el('publicUrl').value=ep.publicUrl||''; el('apiKey').value=ep.apiKey||''; selectedId=ep.id; el('formTitle').textContent='Edit Endpoint'; renderList(); }

    async function loadEndpoints(){
      status('Refreshing endpoint metrics...');
      const res = await fetch('/v1/stash-endpoints?refresh=1');
      const out = await res.json();
      if (!res.ok){ status(out.error || 'Refresh failed','err'); return; }
      endpoints = out;
      renderList();
      renderStashIndexOptions();
      status('Metrics refreshed','ok');

      const drawer = el('settingsDrawer');
      if (endpoints.length === 0) drawer.classList.remove('collapsed');
      else drawer.classList.add('collapsed');
    }

    function renderStashIndexOptions(){
      const sel = el('stashIndex');
      const prev = sel.value;
      sel.innerHTML = '';
      const all = document.createElement('option');
      all.value = '-1';
      all.textContent = 'All';
      sel.appendChild(all);
      endpoints.forEach((ep, i) => {
        const opt = document.createElement('option');
        opt.value = String(i);
        opt.textContent = ep.name;
        sel.appendChild(opt);
      });
      sel.value = [...sel.options].some(o => o.value === prev) ? prev : '-1';
      updateCurl();
    }

    function renderList(){
      const list = el('list'); list.innerHTML='';
      for (const ep of endpoints){
        const li=document.createElement('li');
        const row=document.createElement('div'); row.className='item';
        const name=document.createElement('span'); name.className='item-name'; name.textContent=ep.name; name.title=versionTitle(ep);
        name.onmouseenter = async () => {
          if (versionLoaded.has(ep.id)) return;
          try {
            const res = await fetch('/v1/stash-endpoints/' + ep.id + '/version');
            const out = await res.json();
            if (res.ok && out.version){ versionLoaded.add(ep.id); ep.version = out.version; name.title = versionTitle(ep); }
          } catch(_) {}
        };
        const count=document.createElement('span'); count.className='item-count'; count.textContent=fmtCount(ep.sceneCount); count.title=countTitle(ep);
        row.appendChild(name); row.appendChild(count); li.appendChild(row);
        if (ep.id===selectedId) li.classList.add('active');
        li.onclick=()=>fillForm(ep);
        list.appendChild(li);
      }
    }

    async function saveEndpoint(){
      status('Validating endpoint...');
      const body={ name:el('name').value.trim(), graphqlUrl:el('graphqlUrl').value.trim(), publicUrl:el('publicUrl').value.trim(), apiKey:el('apiKey').value.trim() };
      if (!body.name || !body.graphqlUrl){ status('Name and GraphQL Url are required','err'); return; }
      const url = selectedId ? '/v1/stash-endpoints/' + selectedId : '/v1/stash-endpoints';
      const method = selectedId ? 'PUT' : 'POST';
      const res = await fetch(url,{ method, headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) });
      const out = await res.json();
      if (!res.ok){ status(out.error || 'Save failed','err'); return; }
      status('Saved and validated: ' + out.name + ' ' + prettyVersion(out.version), 'ok');
      await loadEndpoints();
      fillForm(out);
    }

    async function testEndpoint(){
      status('Testing endpoint...');
      const body={ name:el('name').value.trim(), graphqlUrl:el('graphqlUrl').value.trim(), publicUrl:el('publicUrl').value.trim(), apiKey:el('apiKey').value.trim() };
      if (!body.name || !body.graphqlUrl){ status('Name and GraphQL Url are required','err'); return; }
      const res = await fetch('/v1/stash-endpoints-test',{ method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) });
      const out = await res.json();
      if (!res.ok){ status(out.error || 'Test failed','err'); return; }
      status('Connection OK: ' + body.name + ' ' + prettyVersion(out.version), 'ok');
    }

    async function deleteEndpoint(){
      if (!selectedId){ status('Select an endpoint first','err'); return; }
      const res = await fetch('/v1/stash-endpoints/' + selectedId,{ method:'DELETE' });
      const out = await res.json();
      if (!res.ok){ status(out.error || 'Delete failed','err'); return; }
      status('Deleted','ok');
      await loadEndpoints();
      clearForm();
    }

    function updateCurl(){
      if (!(selectedEntry && !selectedEntry.isDir)) {
        el('curlCmd').textContent = 'Select a file to generate curl command.';
        return;
      }
      const stashIndex = Number(el('stashIndex').value || -1);
      const maxTimeDelta = clampInt(el('maxTimeDelta').value, 1, 0, 15);
      const maxDistance = Number(el('maxDistance').value || 0);
      const payload = { filePath: selectedEntry.path };
      if (stashIndex !== -1) payload.stashIndex = stashIndex;
      if (maxTimeDelta !== 1) payload.maxTimeDelta = maxTimeDelta;
      if (maxDistance !== 0) payload.maxDistance = maxDistance;
      const baseUrl = window.location.origin || 'http://localhost:9995';
      const cmd = 'curl -s -X POST ' + baseUrl + '/v1/phash-match -H "Content-Type: application/json" --data \'' + JSON.stringify(payload) + '\'';
      el('curlCmd').textContent = cmd;
    }

    function sabScriptURL(endpointURL){
      const stashIndex = Number(el('stashIndex').value || -1);
      const maxTimeDelta = clampInt(el('maxTimeDelta').value, 1, 0, 15);
      const maxDistance = Number(el('maxDistance').value || 0);
      const hasharrUrl = String(endpointURL || '').trim();
      const q = new URLSearchParams();
      q.set('stashIndex', String(stashIndex));
      q.set('maxTimeDelta', String(maxTimeDelta));
      q.set('maxDistance', String(maxDistance));
      if (hasharrUrl) q.set('hasharrUrl', hasharrUrl);
      return '/v1/sab-postprocess.py?' + q.toString();
    }

    function openSABModal(){
      const modal = el('sabModal');
      const input = el('sabModalEndpoint');
      input.value = 'https://hasharr:9995';
      modal.classList.remove('hidden');
      setTimeout(() => input.focus(), 0);
    }

    function closeSABModal(){
      el('sabModal').classList.add('hidden');
    }

    function clampInt(v, fallback, min, max){
      const n = Number.parseInt(String(v ?? ''), 10);
      if (!Number.isFinite(n)) return fallback;
      if (n < min) return min;
      if (n > max) return max;
      return n;
    }

    async function fetchSceneCard(endpointUrl, sceneId){
      const res = await fetch('/v1/scene-card', {
        method: 'POST',
        headers: {'Content-Type':'application/json'},
        body: JSON.stringify({ endpointUrl, sceneId })
      });
      const out = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(out.error || 'scene card fetch failed');
      return out;
    }

    function sceneURL(publicUrl, sceneId){
      const base = String(publicUrl || '').replace(/\/+$/, '').replace(/\/graphql$/, '');
      if (!base || !sceneId) return '';
      return base + '/scenes/' + encodeURIComponent(sceneId);
    }

    function sceneImageURL(publicUrl, sceneId){
      const base = String(publicUrl || '').replace(/\/+$/, '').replace(/\/graphql$/, '');
      if (!base || !sceneId) return '';
      return base + '/scene/' + encodeURIComponent(sceneId) + '/screenshot';
    }

    function scenePreviewURL(publicUrl, sceneId){
      const base = String(publicUrl || '').replace(/\/+$/, '').replace(/\/graphql$/, '');
      if (!base || !sceneId) return '';
      return base + '/scene/' + encodeURIComponent(sceneId) + '/preview';
    }

    function studioImageURL(publicUrl, studioId){
      const base = String(publicUrl || '').replace(/\/+$/, '').replace(/\/graphql$/, '');
      if (!base || !studioId) return '';
      return base + '/studio/' + encodeURIComponent(studioId) + '/image';
    }

    function fmtDuration(sec){
      const s = Math.max(0, Number(sec || 0));
      const h = Math.floor(s / 3600);
      const m = Math.floor((s % 3600) / 60);
      const r = Math.floor(s % 60);
      if (h > 0) return String(h) + ':' + String(m).padStart(2, '0') + ':' + String(r).padStart(2, '0');
      return String(m) + ':' + String(r).padStart(2, '0');
    }

    function fmtBytes(n){
      const b = Number(n || 0);
      if (!Number.isFinite(b) || b <= 0) return '';
      const units = ['B','KiB','MiB','GiB','TiB'];
      let v = b, i = 0;
      while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
      return (i === 0 ? String(Math.round(v)) : v.toFixed(2).replace(/\.00$/, '')) + ' ' + units[i];
    }

    function fmtBytesSI1(n){
      const b = Number(n || 0);
      if (!Number.isFinite(b) || b <= 0) return '0 B';
      const units = ['B','KB','MB','GB','TB','PB'];
      let v = b, i = 0;
      while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
      return (i === 0 ? String(Math.round(v)) : v.toFixed(1)) + units[i];
    }

    function fmtDate(s){
      const v = String(s || '').trim();
      if (!v) return '';
      const d = new Date(v);
      if (Number.isNaN(d.getTime())) return v;
      return d.toLocaleString();
    }

    function fmtISODate(s){
      const v = String(s || '').trim();
      if (!v) return '';
      const d = new Date(v);
      if (Number.isNaN(d.getTime())) return v;
      return d.toISOString();
    }

    function fmtSceneDate(s){
      const v = String(s || '').trim();
      if (!v) return '';
      const d = new Date(v);
      if (Number.isNaN(d.getTime())) return v;
      return d.toLocaleDateString();
    }

    function fmtDurationLong(sec){
      const s = Math.max(0, Number(sec || 0));
      if (!Number.isFinite(s) || s <= 0) return '0s';
      const units = [
        { label: 'y', size: 31557600 },
        { label: 'm', size: 2629800 },
        { label: 'd', size: 86400 },
        { label: 'h', size: 3600 },
        { label: 'm', size: 60 },
        { label: 's', size: 1 },
      ];
      let idx = units.findIndex((u) => s >= u.size);
      if (idx === -1) idx = units.length - 1;

      const majorUnit = units[idx];
      const major = Math.floor(s / majorUnit.size);
      if (idx === units.length - 1) return String(major) + majorUnit.label;

      const nextUnit = units[idx + 1];
      const remainder = s - (major * majorUnit.size);
      const nextRaw = remainder / nextUnit.size;
      const nextRounded = Math.floor(nextRaw * 10) / 10;
      if (!(nextRounded > 0)) return String(major) + majorUnit.label;
      return String(major) + majorUnit.label + ' ' + nextRounded.toFixed(1).replace(/\.0$/, '') + nextUnit.label;
    }

    function fmtDurationRaw(sec){
      const s = Math.max(0, Number(sec || 0));
      if (!Number.isFinite(s) || s <= 0) return '0s';
      const units = [
        { label: 'y', size: 31557600 },
        { label: 'm', size: 2629800 },
        { label: 'd', size: 86400 },
        { label: 'h', size: 3600 },
        { label: 'm', size: 60 },
        { label: 's', size: 1 },
      ];
      let remain = Math.floor(s);
      const parts = [];
      for (const u of units) {
        const n = Math.floor(remain / u.size);
        if (n > 0) {
          parts.push(String(n) + u.label);
          remain -= n * u.size;
        }
      }
      return parts.length ? parts.join(' ') : '0s';
    }

    function fmtSince(s){
      const v = String(s || '').trim();
      if (!v) return '-';
      const d = new Date(v);
      if (Number.isNaN(d.getTime())) return v;
      const y = d.getFullYear();
      const m = String(d.getMonth() + 1).padStart(2, '0');
      const day = String(d.getDate()).padStart(2, '0');
      return y + '-' + m + '-' + day;
    }

    async function loadStatsSummary(){
      try {
        const res = await fetch('/v1/stats-summary');
        const out = await res.json().catch(() => ({}));
        if (!res.ok) return;
        if (!aboutDrawerInit) {
          aboutDrawerInit = true;
          const hashCount = Number(out.hashCount || 0);
          if (hashCount <= 0) el('aboutDrawer').classList.remove('collapsed');
        }
        el('statHashCount').textContent = fmtCount(out.hashCount || 0);
        el('statDataSum').textContent = fmtBytesSI1(out.dataBytesSum || 0);
        el('statDeleteCount').textContent = fmtCount(out.deleteCount || 0);
        el('statLCount').textContent = fmtCount(out.lCount || 0);
        el('statFCount').textContent = fmtCount(out.fCount || 0);
        el('statDCount').textContent = fmtCount(out.dCount || 0);
        const videoSec = Number(out.videoDurationSumSec || 0);
        const hashSec = Number(out.hashDurationSumSec || 0);
        el('statVideoSum').textContent = fmtDurationLong(videoSec);
        el('statHashTimeSum').textContent = fmtDurationLong(hashSec);
        el('statVideoSum').title = fmtDurationRaw(videoSec);
        el('statHashTimeSum').title = fmtDurationRaw(hashSec);
        el('statSince').textContent = fmtSince(out.since || '');
      } catch(_) {}
    }

    async function recordUIHashStats(targetPath, result, elapsedSec){
      const hash = (result && result.hash) ? result.hash : {};
      const payload = {
        sabNzoID: 'ui',
        fileName: basename(targetPath || ''),
        fileSizeBytes: Number(selectedEntry && selectedEntry.path === targetPath ? (selectedEntry.size || 0) : 0),
        fileDurationSeconds: Number(hash.duration || 0),
        hashDurationSeconds: Number(elapsedSec || 0),
        outcome: 0
      };
      try {
        await fetch('/v1/record-stats', {
          method:'POST',
          headers:{'Content-Type':'application/json'},
          body: JSON.stringify(payload)
        });
      } catch(_) {}
    }

    function normalizedFPS(v){
      const n = Number(v || 0);
      if (!Number.isFinite(n) || n <= 0) return 0;
      return Math.min(n, 30);
    }

    function drawerSummarySuffix(sourceHash, card){
      if (!sourceHash || !card) return '';
      const out = [];
      const sourceY = Number(sourceHash.resolution_y || 0);
      const stashY = Number(card.resolutionY || 0);
      if (sourceY > 0 && stashY > 0 && sourceY > stashY) out.push('Larger');

      const sourceDur = Number(sourceHash.duration || 0);
      const stashDur = Number(card.duration || 0);
      if (sourceDur > 0 && stashDur > 0 && (sourceDur - stashDur) > 1) out.push('Longer');

      const sourceFPS = normalizedFPS(sourceHash.frame_rate);
      const stashFPS = normalizedFPS(card.frameRate);
      if (sourceFPS > 0 && stashFPS > 0 && sourceFPS > stashFPS) out.push('FPS');

      return out.length ? (' ' + out.join(' | ')) : '';
    }

    function basename(p){
      const s = String(p || '');
      if (!s) return '';
      const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
      return i >= 0 ? s.slice(i + 1) : s;
    }

    function renderFileDetails(file){
      return ''
        + '<div class="kv"><span class="k">Hash:</span><span class="v">' + (file.hash || '') + '</span></div>'
        + '<div class="kv"><span class="k">Duration:</span><span class="v">' + (file.duration ? fmtDuration(file.duration) : '') + '</span></div>'
        + '<div class="kv"><span class="k">Path:</span><span class="v">' + (file.path || '') + '</span></div>'
        + '<div class="kv"><span class="k">File Size:</span><span class="v">' + (fmtBytes(file.fileSize) || '') + '</span></div>'
        + '<div class="kv"><span class="k">File Modified:</span><span class="v">' + (fmtISODate(file.fileModifiedTime) || '') + '</span></div>'
        + '<div class="kv"><span class="k">Dimensions:</span><span class="v">' + ((file.resolutionX && file.resolutionY) ? (file.resolutionX + ' x ' + file.resolutionY) : '') + '</span></div>'
        + '<div class="kv"><span class="k">Frame Rate:</span><span class="v">' + (file.frameRate ? (Number(file.frameRate).toFixed(2) + ' fps') : '') + '</span></div>'
        + '<div class="kv"><span class="k">Bit Rate:</span><span class="v">' + (file.bitRate ? (Number(file.bitRate / 1000000).toFixed(2) + ' mbps') : '') + '</span></div>'
        + '<div class="kv"><span class="k">Video Codec:</span><span class="v">' + (file.videoCodec || '') + '</span></div>'
        + '<div class="kv"><span class="k">Audio Codec:</span><span class="v">' + (file.audioCodec || '') + '</span></div>';
    }

    function renderSceneCard(card, endpointName, endpointUrl, publicUrl, match, sourceHash){
      const iconSVG = {
        tag: '<svg viewBox="0 0 448 512" aria-hidden="true"><path d="M0 80L0 229.5c0 17 6.7 33.3 18.7 45.3l176 176c25 25 65.5 25 90.5 0L418.7 317.3c25-25 25-65.5 0-90.5l-176-176c-12-12-28.3-18.7-45.3-18.7L48 32C21.5 32 0 53.5 0 80zm112 32a32 32 0 1 1 0 64 32 32 0 1 1 0-64z"></path></svg>',
        user: '<svg viewBox="0 0 448 512" aria-hidden="true"><path d="M224 256A128 128 0 1 0 224 0a128 128 0 1 0 0 256zm-45.7 48C79.8 304 0 383.8 0 482.3C0 498.7 13.3 512 29.7 512l388.6 0c16.4 0 29.7-13.3 29.7-29.7C448 383.8 368.2 304 269.7 304l-91.4 0z"></path></svg>',
        marker: '<svg viewBox="0 0 384 512" aria-hidden="true"><path d="M215.7 499.2C267 435 384 279.4 384 192C384 86 298 0 192 0S0 86 0 192c0 87.4 117 243 168.3 307.2c12.3 15.3 35.1 15.3 47.4 0zM192 128a64 64 0 1 1 0 128 64 64 0 1 1 0-128z"></path></svg>',
        film: '<svg viewBox="0 0 512 512" aria-hidden="true"><path d="M0 96C0 60.7 28.7 32 64 32l384 0c35.3 0 64 28.7 64 64l0 320c0 35.3-28.7 64-64 64L64 480c-35.3 0-64-28.7-64-64L0 96zM48 368l0 32c0 8.8 7.2 16 16 16l32 0c8.8 0 16-7.2 16-16l0-32c0-8.8-7.2-16-16-16l-32 0c-8.8 0-16 7.2-16 16zm368-16c-8.8 0-16 7.2-16 16l0 32c0 8.8 7.2 16 16 16l32 0c8.8 0 16-7.2 16-16l0-32c0-8.8-7.2-16-16-16l-32 0zM48 240l0 32c0 8.8 7.2 16 16 16l32 0c8.8 0 16-7.2 16-16l0-32c0-8.8-7.2-16-16-16l-32 0c-8.8 0-16 7.2-16 16zm368-16c-8.8 0-16 7.2-16 16l0 32c0 8.8 7.2 16 16 16l32 0c8.8 0 16-7.2 16-16l0-32c0-8.8-7.2-16-16-16l-32 0zM48 112l0 32c0 8.8 7.2 16 16 16l32 0c8.8 0 16-7.2 16-16l0-32c0-8.8-7.2-16-16-16L64 96c-8.8 0-16 7.2-16 16zM416 96c-8.8 0-16 7.2-16 16l0 32c0 8.8 7.2 16 16 16l32 0c8.8 0 16-7.2 16-16l0-32c0-8.8-7.2-16-16-16l-32 0zM160 128l0 64c0 17.7 14.3 32 32 32l128 0c17.7 0 32-14.3 32-32l0-64c0-17.7-14.3-32-32-32L192 96c-17.7 0-32 14.3-32 32zm32 160c-17.7 0-32 14.3-32 32l0 64c0 17.7 14.3 32 32 32l128 0c17.7 0 32-14.3 32-32l0-64c0-17.7-14.3-32-32-32l-128 0z"></path></svg>',
        ocount: '<svg viewBox="0 0 36 36" aria-hidden="true"><path d="M22.855.758L7.875 7.024l12.537 9.733c2.633 2.224 6.377 2.937 9.77 1.518c4.826-2.018 7.096-7.576 5.072-12.413C33.232 1.024 27.68-1.261 22.855.758zm-9.962 17.924L2.05 10.284L.137 23.529a7.993 7.993 0 0 0 2.958 7.803a8.001 8.001 0 0 0 9.798-12.65zm15.339 7.015l-8.156-4.69l-.033 9.223c-.088 2 .904 3.98 2.75 5.041a5.462 5.462 0 0 0 7.479-2.051c1.499-2.644.589-6.013-2.04-7.523z"></path></svg>',
        stash: '<svg viewBox="0 0 640 512" aria-hidden="true"><path d="M58.9 42.1c3-6.1 9.6-9.6 16.3-8.7L320 64 564.8 33.4c6.7-.8 13.3 2.7 16.3 8.7l41.7 83.4c9 17.9-.6 39.6-19.8 45.1L439.6 217.3c-13.9 4-28.8-1.9-36.2-14.3L320 64 236.6 203c-7.4 12.4-22.3 18.3-36.2 14.3L37.1 170.6c-19.3-5.5-28.8-27.2-19.8-45.1L58.9 42.1zM321.1 128l54.9 91.4c14.9 24.8 44.6 36.6 72.5 28.6L576 211.6v167c0 22-15 41.2-36.4 46.6l-204.1 51c-10.2 2.6-20.9 2.6-31 0l-204.1-51C79 419.7 64 400.5 64 378.5v-167L191.6 248c27.8 8 57.6-3.8 72.5-28.6L318.9 128h2.2z"></path></svg>',
        files: '<svg viewBox="0 0 384 512" aria-hidden="true"><path d="M64 0C28.7 0 0 28.7 0 64L0 448c0 35.3 28.7 64 64 64l256 0c35.3 0 64-28.7 64-64l0-288-128 0c-17.7 0-32-14.3-32-32L224 0 64 0zM256 0l0 128 128 0L256 0z"></path></svg>'
      };
      const perf = (card.performers || []).map((p) =>
        '<span class="scene-performer">' + (p && p.name ? p.name : '') + '</span>'
      ).join('');
      const icons = [];
      if (Number(card.tagCount || 0) > 0) icons.push('<span class="scene-ico">' + iconSVG.tag + '<span>' + Number(card.tagCount) + '</span></span>');
      if (Number(card.performerCount || 0) > 0) icons.push('<span class="scene-ico">' + iconSVG.user + '<span>' + Number(card.performerCount) + '</span></span>');
      if (Number(card.markerCount || 0) > 0) icons.push('<span class="scene-ico">' + iconSVG.marker + '<span>' + Number(card.markerCount) + '</span></span>');
      if (Number(card.groupCount || 0) > 0) icons.push('<span class="scene-ico">' + iconSVG.film + '<span>' + Number(card.groupCount) + '</span></span>');
      if (Number(card.oCount || 0) > 0) icons.push('<span class="scene-ico">' + iconSVG.ocount + '<span>' + Number(card.oCount) + '</span></span>');
      if (Number(card.stashIdCount || 0) > 0) icons.push('<span class="scene-ico">' + iconSVG.stash + '<span>' + Number(card.stashIdCount) + '</span></span>');
      if (Number(card.fileCount || 0) > 1) icons.push('<span class="scene-ico">' + iconSVG.files + '<span>' + Number(card.fileCount) + '</span></span>');
      const details = String(card.details || '').trim();
      const files = Array.isArray(card.files) ? card.files : [];
      const sid = card.id || match.id || '';
      const url = sceneURL(publicUrl || endpointUrl, sid);
      const shot = sceneImageURL(publicUrl || endpointUrl, sid);
      const preview = scenePreviewURL(publicUrl || endpointUrl, sid);
      const studioLogo = studioImageURL(publicUrl || endpointUrl, card.studioId);
      const title = card.title || match.title || '(untitled)';
      const titleHTML = title;
      const drawerSuffix = drawerSummarySuffix(sourceHash, card);
      const primaryFile = {
        hash: card.hash,
        path: card.path,
        fileSize: card.fileSize,
        fileModifiedTime: card.fileModifiedTime,
        resolutionX: card.resolutionX,
        resolutionY: card.resolutionY,
        frameRate: card.frameRate,
        bitRate: card.bitRate,
        videoCodec: card.videoCodec,
        audioCodec: card.audioCodec,
        duration: card.duration,
      };
      const drawerFiles = files.length ? files.map((f, i) => {
        if (i !== 0) return f;
        return {
          hash: f.hash || primaryFile.hash,
          path: f.path || primaryFile.path,
          fileSize: f.fileSize || primaryFile.fileSize,
          fileModifiedTime: f.fileModifiedTime || primaryFile.fileModifiedTime,
          resolutionX: f.resolutionX || primaryFile.resolutionX,
          resolutionY: f.resolutionY || primaryFile.resolutionY,
          frameRate: f.frameRate || primaryFile.frameRate,
          bitRate: f.bitRate || primaryFile.bitRate,
          videoCodec: f.videoCodec || primaryFile.videoCodec,
          audioCodec: f.audioCodec || primaryFile.audioCodec,
          duration: f.duration || primaryFile.duration,
        };
      }) : [primaryFile];
      return '<div class="scene-card">'
        + '<div class="scene-media">'
        + (shot ? '<img class="scene-shot" loading="lazy" src="' + shot + '" alt="Scene image" />' : '<div class="scene-shot"></div>')
        + (preview ? '<video class="scene-preview" loop preload="none" muted playsinline src="' + preview + '"></video>' : '')
        + (studioLogo ? '<div class="studio-logo"><img loading="lazy" src="' + studioLogo + '" alt="Studio logo" onerror="this.parentElement.textContent=\'Studio\';" /></div>' : '<div class="studio-logo">Studio</div>')
        + '<div class="scene-overlay"><span class="res">' + ((card.resolutionX && card.resolutionY) ? (card.resolutionY + 'p') : '') + '</span> <span class="dur">' + (card.duration ? fmtDuration(card.duration) : '') + '</span></div>'
        + '<div class="scene-progress"><div></div></div>'
        + '</div>'
        + '<div class="scene-card-section">'
        + (perf ? '<div class="scene-perfs">' + perf + '</div>' : '')
        + '<div class="scene-title">' + titleHTML + '</div>'
        + '<details class="scene-drawer"><summary>ℹ️' + drawerSuffix + '</summary><div class="scene-drawer-body">'
        + '<div class="scene-phash"><span class="k">PHash:</span><span class="v">' + (card.phash || '') + '</span></div>'
        + '<div class="scene-file-list">'
        + drawerFiles.map((f, i) =>
          '<details class="scene-file"' + (i === 0 ? ' open' : '') + '>'
          + '<summary>' + (basename(f.path) || ('File ' + (i + 1))) + (i === 0 ? ' <span style="opacity:.8;">(Primary file)</span>' : '') + '</summary>'
          + '<div class="scene-file-body">' + renderFileDetails(f) + '</div>'
          + '</details>'
        ).join('')
        + '</div>'
        //+ (url ? ('<div><a href="' + url + '" target="_blank" rel="noopener noreferrer">Open scene</a></div>') : '')
        + '</div></details>'
        + '<div class="scene-footer"><span>' + (card.studio || '') + '</span><span>0 views</span><span>' + fmtSceneDate(card.date || '') + '</span></div>'
        + (icons.length ? '<div class="scene-icons">' + icons.join('') + '</div>' : '')
        + '</div>'
        + '</div>';
    }

    function wireScenePreviews(){
      document.querySelectorAll('.scene-card .scene-media').forEach((media) => {
        const v = media.querySelector('.scene-preview');
        const bar = media.querySelector('.scene-progress > div');
        if (!v || !bar) return;
        media.onmouseenter = async () => { try { await v.play(); } catch(_) {} };
        media.onmouseleave = () => { v.pause(); };
        v.ontimeupdate = () => {
          const pct = (v.duration && v.duration > 0) ? Math.min(100, Math.max(0, (v.currentTime / v.duration) * 100)) : 0;
          bar.style.width = pct.toFixed(2) + '%';
        };
      });
    }

    async function renderMatchCards(result){
      const host = el('cards');
      host.innerHTML = '';
      const lookups = result.lookups || [];
      const tasks = [];
      for (const l of lookups){
        const m = l.matches || {};
        const rows = [...(m.exactMatches || []), ...(m.partialMatches || [])];
        for (const row of rows){
          if (!row || !row.id) continue;
          tasks.push({
            endpointName: l.endpointName || '',
            endpointUrl: l.endpointUrl || '',
            publicUrl: l.publicUrl || l.endpointUrl || '',
            match: row,
          });
        }
      }
      if (!tasks.length) {
        host.innerHTML = '<div class="scene-meta">No scene matches found.</div>';
        return;
      }
      const cards = await Promise.all(tasks.map(async (t) => {
        try {
          const card = await fetchSceneCard(t.endpointUrl, t.match.id);
          return renderSceneCard(card, t.endpointName, t.endpointUrl, t.publicUrl, t.match, result.hash || {});
        } catch (e) {
          return '<div class="scene-card"><div class="scene-title">' + (t.match.title || t.match.id) + '</div><div class="scene-meta">Failed to fetch scene card: ' + String(e.message || e) + '</div></div>';
        }
      }));
      host.innerHTML = cards.join('');
      wireScenePreviews();
    }

    async function loadDir(path){
      const q = path ? ('?path=' + encodeURIComponent(path)) : '';
      const res = await fetch('/v1/fs/list' + q);
      const out = await res.json();
      if (!res.ok){ el('resultJson').textContent = JSON.stringify(out, null, 2); return; }
      currentPath = out.path; entries = out.entries || []; selectedEntry = null;
      el('pathInput').value = currentPath;
      renderEntries();
      updateCurl();
    }

    function renderEntries(){
      const tbody = el('fileRows'); tbody.innerHTML='';
      const sorted = [...entries].sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        let cmp = 0;
        if (sortKey === 'size') {
          cmp = Number(a.size || 0) - Number(b.size || 0);
        } else if (sortKey === 'modified') {
          cmp = String(a.modified || '').localeCompare(String(b.modified || ''));
        } else {
          cmp = a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
        }
        if (cmp === 0) cmp = a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
        return sortAsc ? cmp : -cmp;
      });
      for (const ent of sorted){
        const tr = document.createElement('tr');
        if (selectedEntry && selectedEntry.path === ent.path) tr.classList.add('selected');
        tr.innerHTML = '<td>' + (ent.isDir ? '📁 ' : '📄 ') + ent.name + '</td><td>' + (ent.isDir ? '' : fmtCount(ent.size)) + '</td><td>' + ent.modified + '</td>';
        tr.onclick = () => { selectedEntry = ent; el('pathInput').value = ent.path; renderEntries(); updateCurl(); };
        tr.ondblclick = async () => { if (ent.isDir) await loadDir(ent.path); else { selectedEntry = ent; updateCurl(); await runHash(); } };
        tbody.appendChild(tr);
      }
    }

    function updateSortHeadLabels(){
      const n = el('nameSortHead');
      const s = el('sizeSortHead');
      const m = el('modifiedSortHead');
      n.textContent = 'Name' + (sortKey === 'name' ? (sortAsc ? ' ▲' : ' ▼') : '');
      s.textContent = 'Size' + (sortKey === 'size' ? (sortAsc ? ' ▲' : ' ▼') : '');
      m.textContent = 'Date Modified' + (sortKey === 'modified' ? (sortAsc ? ' ▲' : ' ▼') : '');
    }

    function setSort(key){
      if (sortKey === key) sortAsc = !sortAsc;
      else { sortKey = key; sortAsc = true; }
      updateSortHeadLabels();
      renderEntries();
    }

    async function runHash(){
      const target = selectedEntry && !selectedEntry.isDir ? selectedEntry.path : el('pathInput').value.trim();
      if (!target){ el('resultJson').textContent = 'No file selected.'; return; }
      showSpin(true);
      el('resultJson').textContent = 'Working...';
      const startedAt = performance.now();
      const stashIndex = Number(el('stashIndex').value || -1);
      const maxTimeDelta = clampInt(el('maxTimeDelta').value, 1, 0, 15);
      const maxDistance = Number(el('maxDistance').value || 0);
      const payload = { filePath: target };
      if (stashIndex !== -1) payload.stashIndex = stashIndex;
      if (maxTimeDelta !== 1) payload.maxTimeDelta = maxTimeDelta;
      if (maxDistance !== 0) payload.maxDistance = maxDistance;
      const res = await fetch('/v1/phash-match', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(payload) });
      const out = await res.json().catch(()=>({error:'Invalid response'}));
      showSpin(false);
      el('resultJson').textContent = JSON.stringify(out, null, 2);
      if (res.ok) {
        const elapsedSec = Math.max(0, (performance.now() - startedAt) / 1000);
        await recordUIHashStats(target, out, elapsedSec);
        await renderMatchCards(out);
        await loadStatsSummary();
      } else {
        el('cards').innerHTML = '';
      }
    }

    async function upFolder(){
      const base = selectedEntry ? (selectedEntry.isDir ? selectedEntry.path : currentPath) : currentPath;
      let parent = '/';
      if (base && base !== '/') parent = base.substring(0, base.lastIndexOf('/')) || '/';
      await loadDir(parent);
    }

    el('drawerToggle').onclick = () => el('settingsDrawer').classList.toggle('collapsed');
    el('aboutDrawerToggle').onclick = () => el('aboutDrawer').classList.toggle('collapsed');
    el('configDrawerToggle').onclick = () => el('configDrawer').classList.toggle('collapsed');
    el('playgroundDrawerToggle').onclick = () => el('playgroundDrawer').classList.toggle('collapsed');
    el('saveBtn').onclick = saveEndpoint;
    el('testBtn').onclick = testEndpoint;
    el('newBtn').onclick = () => { clearForm(); status(''); };
    el('deleteBtn').onclick = deleteEndpoint;
    el('hashBtn').onclick = runHash;
    el('upBtn').onclick = upFolder;
    el('nameSortHead').onclick = () => setSort('name');
    el('sizeSortHead').onclick = () => setSort('size');
    el('modifiedSortHead').onclick = () => setSort('modified');
    el('rawToggle').onclick = () => el('rawDrawer').classList.toggle('collapsed');
    el('stashIndex').onchange = updateCurl;
    el('maxTimeDelta').onchange = () => { el('maxTimeDelta').value = String(clampInt(el('maxTimeDelta').value, 1, 0, 15)); updateCurl(); };
    el('maxDistance').oninput = () => { el('maxDistanceLabel').textContent = el('maxDistance').value; updateCurl(); };
    el('downloadSabBtn').onclick = openSABModal;
    el('sabModalCancelBtn').onclick = closeSABModal;
    el('sabModalDetectBtn').onclick = () => {
      el('sabModalEndpoint').value = window.location.origin || 'http://hasharr:9995';
    };
    el('sabModalDownloadBtn').onclick = () => {
      const endpointURL = String(el('sabModalEndpoint').value || '').trim();
      window.location.href = sabScriptURL(endpointURL);
      closeSABModal();
    };
    el('sabModal').onclick = (e) => {
      if (e.target && e.target.id === 'sabModal') closeSABModal();
    };
    updateSortHeadLabels();
    el('pathInput').addEventListener('keydown', async (e) => { if (e.key === 'Enter') await loadDir(el('pathInput').value.trim()); });

    Promise.all([loadEndpoints(), loadDir(''), loadStatsSummary()]).catch(err => { status(String(err),'err'); });
  </script>
</body></html>`
