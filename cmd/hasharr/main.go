package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hasharr/internal/phash"
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

var computePHash = phash.Compute
var configStore *stashconfig.Store
var resourcesDir string
var lookupMatches = stashconfig.LookupSceneMatchesWithOptions

func main() {
	addr := envOrDefault("HASHARR_ADDR", ":9995")
	configPath := envOrDefault("HASHARR_CONFIG_FILE", "/config/config.json")
	resourcesDir = envOrDefault("HASHARR_RESOURCES_DIR", "./resources")
	store, err := stashconfig.NewStore(configPath)
	if err != nil {
		log.Fatal(err)
	}
	configStore = store

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.HandleFunc("/favicon-source.png", handleFaviconSource)
	mux.HandleFunc("/logo.png", handleLogo)
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/v1/phash", handlePHash)
	mux.HandleFunc("/v1/phash-match", handlePHashMatch)
	mux.HandleFunc("/v1/fs/list", handleFSList)
	mux.HandleFunc("/v1/stash-endpoints", handleStashEndpoints)
	mux.HandleFunc("/v1/stash-endpoints/", handleStashEndpointByID)
	mux.HandleFunc("/v1/stash-endpoints-test", handleStashEndpointTest)

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
	_, _ = io.WriteString(w, configPageHTML)
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
		EndpointID   string                        `json:"endpointId"`
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
			EndpointID:   ep.ID,
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
		if st, err := os.Stat("/downloaded"); err == nil && st.IsDir() {
			requested = "/downloaded"
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
  <style>
    :root { --bg:#1b1f24; --panel:#232933; --panel2:#1f2430; --text:#d9dde3; --muted:#8a94a6; --accent:#2b9bd6; --accent-hover:#2388bc; --accent-active:#1d759f; --ok:#2ecc71; --err:#ff5f56; --border:#313846; }
    * { box-sizing:border-box; font-family: Inter, system-ui, -apple-system, Segoe UI, Roboto, sans-serif; }
    body { margin:0; background:var(--bg); color:var(--text); }
    .container { max-width:1300px; margin:12px auto 24px; padding:0 16px; }
    .brand { display:flex; align-items:center; gap:12px; margin:8px 0 10px; }
    .brand img { width:64px; height:64px; border-radius:8px; }
    .brand h1 { margin:0; font-size:56px; line-height:1; }
    .panel { background:var(--panel); border:1px solid var(--border); border-radius:10px; padding:10px; }
    .drawer-head { display:flex; align-items:center; justify-content:space-between; cursor:pointer; padding:8px 10px; border-radius:8px; background:var(--panel2); }
    .drawer-title { font-weight:700; }
    .carrot { color:var(--muted); transition:transform .15s ease; user-select:none; }
    .collapsed .carrot { transform:rotate(-90deg); }
    .drawer-body { margin-top:10px; display:grid; grid-template-columns:260px 1fr; gap:12px; }
    .collapsed .drawer-body { display:none; }
    .card { background:var(--panel2); border:1px solid var(--border); border-radius:8px; padding:10px; }
    h2 { margin:0 0 2px; font-size:28px; }
    h3 { margin:0 0 8px; font-size:18px; }
    .sub { color:var(--muted); font-size:12px; text-transform:uppercase; letter-spacing:.05em; margin-bottom:8px; }
    ul { list-style:none; padding:0; margin:0; display:flex; flex-direction:column; gap:8px; max-height:240px; overflow:auto; }
    li { border:1px solid var(--border); border-radius:8px; padding:8px; cursor:pointer; }
    li.active { border-color:var(--accent); background:#2c3340; }
    .item { display:flex; justify-content:space-between; align-items:center; gap:10px; }
    .item-name { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .item-count { min-width:95px; text-align:right; font-variant-numeric: tabular-nums; }
    label { font-size:12px; color:var(--muted); display:block; margin:8px 0 6px; }
    input { width:100%; padding:10px; border:1px solid var(--border); border-radius:8px; background:#12161d; color:var(--text); }
    .row { display:flex; gap:8px; }
    .grow { flex:1; }
    button { margin-top:10px; padding:9px 12px; border-radius:8px; border:1px solid var(--border); background:#2c3340; color:var(--text); cursor:pointer; }
    button.primary { background:var(--accent); color:#fff; border:0; font-weight:700; }
    button.primary:hover { background:var(--accent-hover); }
    button.primary:active { background:var(--accent-active); }
    .status { min-height:20px; margin-top:8px; font-size:13px; }
    .ok { color:var(--ok); } .err { color:var(--err); }
    .usage { margin-top:10px; padding-top:8px; border-top:1px dashed var(--border); color:var(--muted); font-size:12px; }
    .workflow { margin-top:12px; display:grid; grid-template-columns:1fr; gap:10px; }
    .pathbar, .curlbar { background:var(--panel2); border:1px solid var(--border); border-radius:8px; padding:8px; }
    .pathrow { display:flex; gap:8px; align-items:center; }
    .pathrow input { margin:0; }
    input[type="range"] { -webkit-appearance:none; appearance:none; background:transparent; }
    input[type="range"]::-webkit-slider-runnable-track { height:6px; background:#c7ccd5; border-radius:999px; }
    input[type="range"]::-webkit-slider-thumb {
      -webkit-appearance:none; appearance:none; width:16px; height:16px; border-radius:50%;
      background:var(--accent); border:0; margin-top:-5px;
    }
    input[type="range"]::-moz-range-track { height:6px; background:#c7ccd5; border-radius:999px; }
    input[type="range"]::-moz-range-progress { height:6px; background:#c7ccd5; border-radius:999px; }
    input[type="range"]::-moz-range-thumb { width:16px; height:16px; border-radius:50%; background:var(--accent); border:0; }
    .browser-results { display:grid; grid-template-columns:1.2fr .8fr; gap:10px; }
    table { width:100%; border-collapse:collapse; font-size:12px; }
    th, td { border-bottom:1px solid var(--border); padding:6px 8px; text-align:left; }
    tr.selected { background:#2b3442; }
    tr:hover { background:#27303d; cursor:pointer; }
    .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break:break-all; }
    .results { min-height:360px; background:var(--panel2); border:1px solid var(--border); border-radius:8px; padding:10px; display:flex; flex-direction:column; }
    .spinner { height:4px; background:transparent; overflow:hidden; border-radius:3px; visibility:hidden; }
    .spinner.show { visibility:visible; }
    .spinner::before { content:''; display:block; width:35%; height:100%; background:var(--accent); animation:slide 1s linear infinite; }
    @keyframes slide { 0% { margin-left:-35%; } 100% { margin-left:100%; } }
    pre { margin:10px 0 0; white-space:pre-wrap; color:#c9d0dd; }
    @media (max-width: 1000px) { .drawer-body, .browser-results { grid-template-columns:1fr; } .brand h1 { font-size:40px; } }
  </style>
</head>
<body>
  <div class="container">
    <div class="brand">
      <img src="/logo.png" alt="hasharr logo" />
      <h1>hasharr</h1>
    </div>

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
      <div class="pathbar">
        <div class="sub">phash-match configurator <span title="Defaults: stashIndex=-1 (All endpoints), maxTimeDelta=1s, maxDistance=0">ⓘ</span></div>
        <div class="pathrow">
          <label style="margin:0;min-width:72px;">stashIndex</label>
          <select id="stashIndex" style="flex:1; padding:9px; border:1px solid var(--border); border-radius:8px; background:#12161d; color:var(--text);">
            <option value="-1">All</option>
          </select>
          <label style="margin:0;min-width:95px;">maxTimeDelta</label>
          <input id="maxTimeDelta" type="number" min="0" max="15" step="1" value="1" style="width:90px;" />
          <label style="margin:0;min-width:88px;">maxDistance</label>
          <input id="maxDistance" type="range" min="0" max="8" step="1" value="0" style="width:130px;" />
          <span id="maxDistanceLabel" style="min-width:14px; text-align:right;">0</span>
        </div>
      </div>
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
            <thead><tr><th>Name</th><th>Size</th><th>Date Modified</th></tr></thead>
            <tbody id="fileRows"></tbody>
          </table>
        </div>
        <div class="results">
          <div class="sub">results</div>
          <div class="spinner" id="spinner"></div>
          <pre id="resultJson">Results</pre>
        </div>
      </div>
    </section>
  </div>

  <script>
    let endpoints = [];
    const versionLoaded = new Set();
    let selectedId = null;
    let currentPath = '';
    let selectedEntry = null;
    let entries = [];
    const el = (id) => document.getElementById(id);
    const status = (msg, cls='') => { el('status').className = 'status ' + cls; el('status').textContent = msg; };
    const showSpin = (on) => el('spinner').classList.toggle('show', !!on);
    const prettyVersion = (v) => { const s=String(v||'').trim(); return !s ? '' : (s[0]==='v'||s[0]==='V'?s:('v'+s)); };
    const fmtCount = (n) => Number(n||0).toLocaleString();
    const versionTitle = (ep) => 'Version: ' + prettyVersion(ep.version);
    const countTitle = (ep) => Number(ep.phashPercent||0).toFixed(2) + '% phashes.  ' + fmtCount(ep.sceneCount) + ' of ' + fmtCount(ep.totalSceneCount) + ' scenes';

    function clearForm(){ el('name').value=''; el('graphqlUrl').value=''; el('apiKey').value=''; selectedId=null; el('formTitle').textContent='Add Endpoint'; renderList(); }
    function fillForm(ep){ el('name').value=ep.name; el('graphqlUrl').value=ep.graphqlUrl; el('apiKey').value=ep.apiKey||''; selectedId=ep.id; el('formTitle').textContent='Edit Endpoint'; renderList(); }

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
      const body={ name:el('name').value.trim(), graphqlUrl:el('graphqlUrl').value.trim(), apiKey:el('apiKey').value.trim() };
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
      const body={ name:el('name').value.trim(), graphqlUrl:el('graphqlUrl').value.trim(), apiKey:el('apiKey').value.trim() };
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

    function clampInt(v, fallback, min, max){
      const n = Number.parseInt(String(v ?? ''), 10);
      if (!Number.isFinite(n)) return fallback;
      if (n < min) return min;
      if (n > max) return max;
      return n;
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
      for (const ent of entries){
        const tr = document.createElement('tr');
        if (selectedEntry && selectedEntry.path === ent.path) tr.classList.add('selected');
        tr.innerHTML = '<td>' + (ent.isDir ? '📁 ' : '📄 ') + ent.name + '</td><td>' + (ent.isDir ? '' : fmtCount(ent.size)) + '</td><td>' + ent.modified + '</td>';
        tr.onclick = () => { selectedEntry = ent; el('pathInput').value = ent.path; renderEntries(); updateCurl(); };
        tr.ondblclick = async () => { if (ent.isDir) await loadDir(ent.path); else { selectedEntry = ent; updateCurl(); await runHash(); } };
        tbody.appendChild(tr);
      }
    }

    async function runHash(){
      const target = selectedEntry && !selectedEntry.isDir ? selectedEntry.path : el('pathInput').value.trim();
      if (!target){ el('resultJson').textContent = 'No file selected.'; return; }
      showSpin(true);
      el('resultJson').textContent = 'Working...';
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
    }

    async function upFolder(){
      const base = selectedEntry ? (selectedEntry.isDir ? selectedEntry.path : currentPath) : currentPath;
      let parent = '/';
      if (base && base !== '/') parent = base.substring(0, base.lastIndexOf('/')) || '/';
      await loadDir(parent);
    }

    el('drawerToggle').onclick = () => el('settingsDrawer').classList.toggle('collapsed');
    el('saveBtn').onclick = saveEndpoint;
    el('testBtn').onclick = testEndpoint;
    el('newBtn').onclick = () => { clearForm(); status(''); };
    el('deleteBtn').onclick = deleteEndpoint;
    el('hashBtn').onclick = runHash;
    el('upBtn').onclick = upFolder;
    el('stashIndex').onchange = updateCurl;
    el('maxTimeDelta').onchange = () => { el('maxTimeDelta').value = String(clampInt(el('maxTimeDelta').value, 1, 0, 15)); updateCurl(); };
    el('maxDistance').oninput = () => { el('maxDistanceLabel').textContent = el('maxDistance').value; updateCurl(); };
    el('pathInput').addEventListener('keydown', async (e) => { if (e.key === 'Enter') await loadDir(el('pathInput').value.trim()); });

    Promise.all([loadEndpoints(), loadDir('')]).catch(err => { status(String(err),'err'); });
  </script>
</body></html>`
