package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"hasharr/internal/phash"
	"hasharr/internal/stashconfig"
)

type requestBody struct {
	Path string `json:"path"`
}

type errorResponse struct {
	Error string `json:"error"`
}

var computePHash = phash.Compute
var configStore *stashconfig.Store
var resourcesDir string

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
	mux.HandleFunc("/logo.png", handleLogo)
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/v1/phash", handlePHash)
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
	http.ServeFile(w, r, resourcesDir+"/favicon.ico")
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
	id := strings.TrimPrefix(r.URL.Path, "/v1/stash-endpoints/")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing endpoint id")
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
  <title>hasharr Configuration</title>
  <link rel="icon" href="/favicon.ico" sizes="32x32" />
  <style>
    :root { --bg:#1b1f24; --panel:#232933; --text:#d9dde3; --muted:#8a94a6; --accent:#2b9bd6; --accent-hover:#2388bc; --accent-active:#1d759f; --ok:#2ecc71; --err:#ff5f56; --border:#313846; }
    * { box-sizing:border-box; font-family: Inter, system-ui, -apple-system, Segoe UI, Roboto, sans-serif; }
    body { margin:0; background:var(--bg); color:var(--text); }
    .wrap { max-width:1000px; margin:28px auto; padding:0 16px; display:grid; grid-template-columns:320px 1fr; gap:16px; }
    .brand { max-width:1000px; margin:22px auto 0; padding:0 16px; display:flex; align-items:center; gap:12px; }
    .brand img { width:36px; height:36px; border-radius:8px; }
    .brand h1 { margin:0; font-size:22px; }
    .card { background:var(--panel); border:1px solid var(--border); border-radius:10px; padding:14px; }
    h1 { margin:0 0 4px; font-size:20px; }
    h2 { margin:0 0 12px; font-size:14px; color:var(--muted); font-weight:600; text-transform:uppercase; letter-spacing:.04em; }
    ul { list-style:none; padding:0; margin:0; display:flex; flex-direction:column; gap:8px; }
    li { border:1px solid var(--border); border-radius:8px; padding:10px; cursor:pointer; }
    li.active { border-color:var(--accent); background:#2c3340; }
    .row { display:flex; gap:8px; }
    .grow { flex:1; }
    label { font-size:12px; color:var(--muted); display:block; margin:8px 0 6px; }
    input { width:100%; padding:10px; border:1px solid var(--border); border-radius:8px; background:#12161d; color:var(--text); }
    button { margin-top:12px; padding:10px 12px; border-radius:8px; border:1px solid var(--border); background:#2c3340; color:var(--text); cursor:pointer; }
    button.primary { background:var(--accent); color:#ffffff; border:0; font-weight:700; }
    button.primary:hover { background:var(--accent-hover); }
    button.primary:active { background:var(--accent-active); }
    .status { min-height:22px; margin-top:10px; font-size:13px; }
    .ok { color:var(--ok); } .err { color:var(--err); }
    .tiny { color:var(--muted); font-size:12px; margin-top:8px; }
    @media (max-width: 900px) { .wrap { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <div class="brand">
    <img src="/logo.png" alt="hasharr logo" />
    <h1>hasharr</h1>
  </div>
  <div class="wrap">
    <section class="card">
      <h1>Stash Endpoints</h1>
      <h2>Configured Instances</h2>
      <ul id="list"></ul>
    </section>
    <section class="card">
      <h1 id="formTitle">Add Endpoint</h1>
      <h2>Validated on Save</h2>
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
      <div class="tiny">Display format: <strong>Name vVersion</strong> after endpoint validation succeeds.</div>
    </section>
  </div>
  <script>
    let endpoints = [];
    let selectedId = null;
    const el = (id) => document.getElementById(id);
    const status = (msg, cls='') => { el('status').className = 'status ' + cls; el('status').textContent = msg; };
    function clearForm(){ el('name').value=''; el('graphqlUrl').value=''; el('apiKey').value=''; selectedId=null; el('formTitle').textContent='Add Endpoint'; renderList(); }
    function fillForm(ep){ el('name').value=ep.name; el('graphqlUrl').value=ep.graphqlUrl; el('apiKey').value=ep.apiKey||''; selectedId=ep.id; el('formTitle').textContent='Edit Endpoint'; renderList(); }
    function prettyVersion(v){
      const s = String(v || '').trim();
      if (!s) return '';
      return (s.startsWith('v') || s.startsWith('V')) ? s : ('v' + s);
    }
    function line(ep){ return ep.name + '  ' + prettyVersion(ep.version); }
    function renderList(){
      const list = el('list'); list.innerHTML='';
      for (const ep of endpoints){
        const li=document.createElement('li');
        li.textContent=line(ep);
        if (ep.id===selectedId) li.classList.add('active');
        li.onclick=()=>fillForm(ep);
        list.appendChild(li);
      }
    }
    async function load(){
      const res=await fetch('/v1/stash-endpoints');
      endpoints=await res.json();
      renderList();
    }
    async function save(){
      status('Validating endpoint...');
      const body={ name:el('name').value.trim(), graphqlUrl:el('graphqlUrl').value.trim(), apiKey:el('apiKey').value.trim() };
      if (!body.name || !body.graphqlUrl){ status('Name and GraphQL Url are required','err'); return; }
      const url = selectedId ? '/v1/stash-endpoints/' + selectedId : '/v1/stash-endpoints';
      const method = selectedId ? 'PUT' : 'POST';
      const res = await fetch(url,{ method, headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) });
      const out = await res.json();
      if (!res.ok){ status(out.error || 'Save failed','err'); return; }
      status('Saved and validated: ' + line(out), 'ok');
      await load();
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
    async function del(){
      if (!selectedId){ status('Select an endpoint first','err'); return; }
      const res = await fetch('/v1/stash-endpoints/' + selectedId,{ method:'DELETE' });
      const out = await res.json();
      if (!res.ok){ status(out.error || 'Delete failed','err'); return; }
      status('Deleted', 'ok');
      await load();
      clearForm();
    }
    el('saveBtn').onclick=save;
    el('testBtn').onclick=testEndpoint;
    el('newBtn').onclick=()=>{ clearForm(); status(''); };
    el('deleteBtn').onclick=del;
    load().catch(err=>status(String(err),'err'));
  </script>
</body></html>`
