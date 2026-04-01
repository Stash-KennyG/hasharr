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

	"hasharr/internal/hashservice"
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

type hashServiceRunRequest struct {
	FilePath string `json:"filePath"`
	Source   string `json:"source,omitempty"`
	JobID    string `json:"jobId,omitempty"`
}

type hashServiceProfileRequest struct {
	Name         string  `json:"name"`
	Enabled      bool    `json:"enabled"`
	RemotePath   string  `json:"remotePath"`
	HasharrPath  string  `json:"hasharrPath"`
	StashIndex   int     `json:"stashIndex"`
	MaxTimeDelta float64 `json:"maxTimeDelta"`
	MaxDistance  int     `json:"maxDistance"`
	ApplyActions bool    `json:"applyActions"`
}

var computePHash = phash.Compute
var configStore *stashconfig.Store
var statsStore *recordstats.Store
var hashServiceStore *hashservice.Store
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
	hsStore, err := hashservice.New(statsPath)
	if err != nil {
		log.Fatal(err)
	}
	hashServiceStore = hsStore

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/settings/stash", handleSettingsStash)
	mux.HandleFunc("/settings/python", handleSettingsPython)
	mux.HandleFunc("/settings/metube", handleSettingsMeTube)
	mux.HandleFunc("/settings/integrations", handleSettingsIntegrations)
	mux.HandleFunc("/logs", handleLogsPage)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.HandleFunc("/favicon-source.png", handleFaviconSource)
	mux.HandleFunc("/logo.png", handleLogo)
	mux.HandleFunc("/app.css", handleAppCSS)
	mux.HandleFunc("/v1/sab-postprocess.py", handleSABPostProcessScript)
	mux.HandleFunc("/v1/hash-service-client.py", handleHashServiceClientScript)
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
	mux.HandleFunc("/v1/stats-logs", handleRecordStatsLogs)
	mux.HandleFunc("/v1/stats-logs/clear", handleRecordStatsLogsClear)
	mux.HandleFunc("/api/hash-service/", handleHashServiceRun)
	mux.HandleFunc("/v1/hash-service-profiles", handleHashServiceProfiles)
	mux.HandleFunc("/v1/hash-service-profiles/", handleHashServiceProfileByID)

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
	renderMainPage(w, "dashboard")
}

func handleSettingsStash(w http.ResponseWriter, r *http.Request) {
	renderMainPage(w, "stash")
}

func handleSettingsPython(w http.ResponseWriter, r *http.Request) {
	renderMainPage(w, "integrations")
}

func handleSettingsMeTube(w http.ResponseWriter, r *http.Request) {
	renderMainPage(w, "integrations")
}

func handleSettingsIntegrations(w http.ResponseWriter, r *http.Request) {
	renderMainPage(w, "integrations")
}

func loadHTMLTemplate(name string) (string, error) {
	path := filepath.Join(resourcesDir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func renderMainPage(w http.ResponseWriter, section string) {
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
	tmpl, err := loadHTMLTemplate("app.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load app template")
		return
	}
	html := strings.ReplaceAll(tmpl, "__HASHARR_VERSION__", versionText)
	html = strings.ReplaceAll(html, "__HASHARR_VERSION_TOOLTIP__", versionTip)
	html = strings.ReplaceAll(html, "__PAGE_BOOTSTRAP__", section)
	_, _ = io.WriteString(w, html)
}

func handleLogsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/logs" {
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
	tmpl, err := loadHTMLTemplate("logs.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load logs template")
		return
	}
	html := strings.ReplaceAll(tmpl, "__HASHARR_VERSION__", versionText)
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

func handleHashServiceClientScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	serviceID := clampIntQuery(r.URL.Query().Get("serviceID"), 1, 1, 1000000)
	hasharrURL := strings.TrimSpace(r.URL.Query().Get("hasharrUrl"))
	if hasharrURL == "" {
		hasharrURL = strings.TrimSpace(r.Header.Get("Origin"))
	}
	if hasharrURL == "" {
		hasharrURL = "http://hasharr:9995"
	}
	script := "#!/usr/bin/env python3\n" +
		"\"\"\"hasharr hash-service client script.\n\n" +
		"Calls hasharr POST /api/hash-service/{id} for files found in SAB complete dir.\n" +
		"\"\"\"\n" +
		"from __future__ import annotations\n\n" +
		"import json\n" +
		"import os\n" +
		"import pathlib\n" +
		"import urllib.request\n" +
		"from typing import Iterable\n\n" +
		"DEFAULT_HASHARR_URL = " + strconv.Quote(hasharrURL) + "\n" +
		"DEFAULT_SERVICE_ID = " + strconv.Itoa(serviceID) + "\n\n" +
		"VIDEO_EXTS = {'.mp4','.mkv','.avi','.wmv','.mov','.webm','.m4v','.ts','.m2ts','.flv'}\n\n" +
		"def iter_video_files(root: pathlib.Path) -> Iterable[pathlib.Path]:\n" +
		"    for p in root.rglob('*'):\n" +
		"        if not p.is_file():\n" +
		"            continue\n" +
		"        if p.suffix.lower() in VIDEO_EXTS:\n" +
		"            yield p\n\n" +
		"def call_hasharr(file_path: pathlib.Path) -> int:\n" +
		"    payload = {\n" +
		"        'filePath': str(file_path),\n" +
		"        'source': 'python-client',\n" +
		"        'jobId': os.environ.get('SAB_NZO_ID',''),\n" +
		"    }\n" +
		"    data = json.dumps(payload).encode('utf-8')\n" +
		"    req = urllib.request.Request(\n" +
		"        f\"{DEFAULT_HASHARR_URL.rstrip('/')}/api/hash-service/{DEFAULT_SERVICE_ID}\",\n" +
		"        data=data,\n" +
		"        headers={'Content-Type':'application/json'},\n" +
		"        method='POST',\n" +
		"    )\n" +
		"    with urllib.request.urlopen(req, timeout=30) as resp:\n" +
		"        return int(resp.status)\n\n" +
		"def main() -> int:\n" +
		"    complete_dir = os.environ.get('SAB_COMPLETE_DIR','').strip()\n" +
		"    if not complete_dir:\n" +
		"        print('[hasharr-client] SAB_COMPLETE_DIR missing')\n" +
		"        return 0\n" +
		"    root = pathlib.Path(complete_dir)\n" +
		"    if not root.exists():\n" +
		"        print(f'[hasharr-client] directory missing: {root}')\n" +
		"        return 0\n" +
		"    files = list(iter_video_files(root))\n" +
		"    if not files:\n" +
		"        print('[hasharr-client] no video files found')\n" +
		"        return 0\n" +
		"    for f in files:\n" +
		"        try:\n" +
		"            code = call_hasharr(f)\n" +
		"            print(f'[hasharr-client] {f.name}: status={code}')\n" +
		"        except Exception as exc:\n" +
		"            print(f'[hasharr-client] error for {f}: {exc}')\n" +
		"    return 0\n\n" +
		"if __name__ == '__main__':\n" +
		"    raise SystemExit(main())\n"

	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="hash_service_client.py"`)
	_, _ = io.WriteString(w, script)
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

func handleRecordStatsLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page := clampIntQuery(r.URL.Query().Get("page"), 1, 1, 1000000)
	pageSize := clampIntQuery(r.URL.Query().Get("pageSize"), 100, 1, 100)
	out, err := statsStore.Logs(r.Context(), page, pageSize)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func handleRecordStatsLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := statsStore.Clear(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleHashServiceProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := hashServiceStore.List(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rows)
	case http.MethodPost:
		var req hashServiceProfileRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json body")
			return
		}
		out, err := hashServiceStore.Upsert(r.Context(), hashservice.Profile{
			Name:         strings.TrimSpace(req.Name),
			Enabled:      req.Enabled,
			RemotePath:   strings.TrimSpace(req.RemotePath),
			HasharrPath:  strings.TrimSpace(req.HasharrPath),
			StashIndex:   req.StashIndex,
			MaxTimeDelta: req.MaxTimeDelta,
			MaxDistance:  req.MaxDistance,
			ApplyActions: req.ApplyActions,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, out)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleHashServiceProfileByID(w http.ResponseWriter, r *http.Request) {
	idRaw := strings.TrimPrefix(r.URL.Path, "/v1/hash-service-profiles/")
	idRaw = strings.TrimSpace(idRaw)
	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid profile id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		out, err := hashServiceStore.Get(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "profile not found")
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPut:
		var req hashServiceProfileRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json body")
			return
		}
		out, err := hashServiceStore.Upsert(r.Context(), hashservice.Profile{
			ID:           id,
			Name:         strings.TrimSpace(req.Name),
			Enabled:      req.Enabled,
			RemotePath:   strings.TrimSpace(req.RemotePath),
			HasharrPath:  strings.TrimSpace(req.HasharrPath),
			StashIndex:   req.StashIndex,
			MaxTimeDelta: req.MaxTimeDelta,
			MaxDistance:  req.MaxDistance,
			ApplyActions: req.ApplyActions,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		if err := hashServiceStore.Delete(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleHashServiceRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idRaw := strings.TrimPrefix(r.URL.Path, "/api/hash-service/")
	idRaw = strings.TrimSpace(idRaw)
	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid service id")
		return
	}
	profile, err := hashServiceStore.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "hash service profile not found")
		return
	}
	if !profile.Enabled {
		writeErr(w, http.StatusBadRequest, "hash service profile is disabled")
		return
	}

	var req hashServiceRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	if req.FilePath == "" {
		writeErr(w, http.StatusBadRequest, "filePath is required")
		return
	}
	if profile.RemotePath != "" && profile.HasharrPath != "" && strings.HasPrefix(req.FilePath, profile.RemotePath) {
		req.FilePath = profile.HasharrPath + strings.TrimPrefix(req.FilePath, profile.RemotePath)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	hashResult, err := computePHash(ctx, req.FilePath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	endpoints := configStore.List()
	selected := endpoints
	if profile.StashIndex >= 0 {
		if profile.StashIndex >= len(endpoints) {
			writeErr(w, http.StatusBadRequest, "profile stashIndex out of range")
			return
		}
		selected = []stashconfig.Endpoint{endpoints[profile.StashIndex]}
	}
	if len(selected) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"profileId":     profile.ID,
			"lookupSkipped": "no endpoints configured",
			"filePath":      req.FilePath,
		})
		return
	}

	type endpointLookup struct {
		EndpointURL  string                        `json:"endpointUrl"`
		PublicURL    string                        `json:"publicUrl"`
		EndpointName string                        `json:"endpointName"`
		APIKey       string                        `json:"-"`
		Matches      stashconfig.SceneLookupResult `json:"matches"`
	}
	lookups := []endpointLookup{}
	totalExact := 0
	totalPartial := 0
	for _, ep := range selected {
		lctx, lcancel := context.WithTimeout(r.Context(), 40*time.Second)
		lookup, err := lookupMatches(
			lctx,
			&http.Client{Timeout: 20 * time.Second},
			ep.GraphQLURL,
			ep.APIKey,
			hashResult.PHash,
			hashResult.Duration,
			profile.MaxTimeDelta,
			profile.MaxDistance,
		)
		lcancel()
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		totalExact += len(lookup.ExactMatches)
		totalPartial += len(lookup.PartialMatches)
		lookups = append(lookups, endpointLookup{
			EndpointURL:  ep.GraphQLURL,
			PublicURL:    ep.PublicURL,
			EndpointName: ep.Name,
			APIKey:       ep.APIKey,
			Matches:      lookup,
		})
	}

	action := "none"
	outcome := 0
	renamedTo := ""
	reasons := []string{}
	if profile.ApplyActions && totalExact > 0 {
		maxY := 0
		maxDur := 0.0
		maxFPS := 0.0
		for _, l := range lookups {
			ep := l.EndpointURL
			for _, m := range l.Matches.ExactMatches {
				card, err := stashconfig.QuerySceneCard(r.Context(), &http.Client{Timeout: 20 * time.Second}, ep, l.APIKey, m.ID)
				if err != nil {
					continue
				}
				if card.ResolutionY > maxY {
					maxY = card.ResolutionY
				}
				if card.Duration > maxDur {
					maxDur = card.Duration
				}
				f := card.FrameRate
				if f > 30 {
					f = 30
				}
				if f > maxFPS {
					maxFPS = f
				}
			}
		}
		srcFPS := hashResult.FrameRate
		if srcFPS > 30 {
			srcFPS = 30
		}
		if hashResult.ResolutionY > maxY {
			reasons = append(reasons, "Larger Resolution Detected")
			outcome |= 4
		}
		if (hashResult.Duration - maxDur) > 1 {
			reasons = append(reasons, "Longer Duration Detected")
			outcome |= 2
		}
		if srcFPS > maxFPS {
			reasons = append(reasons, "Higher FPS Detected")
			outcome |= 1
		}
		if len(reasons) == 0 {
			action = "delete"
			outcome |= 8
			_ = os.Remove(req.FilePath)
		} else {
			action = "tag-exact"
			prefix := "["
			if (outcome & 4) != 0 {
				prefix += "L"
			}
			if (outcome & 2) != 0 {
				prefix += "D"
			}
			if (outcome & 1) != 0 {
				prefix += "F"
			}
			prefix += "]"
			dir := filepath.Dir(req.FilePath)
			base := filepath.Base(req.FilePath)
			target := filepath.Join(dir, prefix+base)
			if err := os.Rename(req.FilePath, target); err == nil {
				renamedTo = target
			}
		}
	} else if totalPartial > 0 {
		action = "tag-potential"
	}

	_ = statsStore.Insert(r.Context(), recordstats.Record{
		SABNzoID:            strings.TrimSpace(req.Source + ":" + req.JobID),
		FileName:            filepath.Base(req.FilePath),
		FileSizeBytes:       0,
		FileDurationSeconds: hashResult.Duration,
		HashDurationSeconds: 0,
		Outcome:             outcome,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"profileId":    profile.ID,
		"filePath":     req.FilePath,
		"hash":         hashResult,
		"lookups":      lookups,
		"exactCount":   totalExact,
		"partialCount": totalPartial,
		"action":       action,
		"reasons":      reasons,
		"outcomeCode":  outcome,
		"renamedTo":    renamedTo,
		"deleted":      action == "delete",
	})
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
