# hasharr

[![CI](https://github.com/Stash-KennyG/hasharr/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Stash-KennyG/hasharr/actions/workflows/ci.yml)

`hasharr` is a container-friendly web service that computes a Stash-style video perceptual hash (`phash`) and basic media metadata from a local file path.

## Design Intent

`hasharr` is built to reduce manual Usenet cleanup work:

- hash completed downloads quickly
- compare against one or more Stash libraries using perceptual matching
- automatically delete or tag likely duplicates before manual categorization
- surface quality signals (resolution, duration, fps) and processing stats over time

The core goal is simple: spend less time filtering duplicates, and more time on high-value curation.

## Getting Started (SABnzbd workflow)

1. Run `hasharr` (Docker example in the next section) and open `http://localhost:9995/`.
2. In `⚙️ Settings`, add your Stash endpoint(s) and save.
3. In `🐍 Configurator`, choose matching defaults:
   - `Stash Endpoints` (`All` or a specific endpoint)
   - `maxTimeDelta`
   - `maxDistance`
4. Click `Download Script` and, in the modal:
   - set endpoint URL for your SAB runtime (container deployments typically use `http://hasharr:9995`)
   - optionally click `Detect URL`
   - click `Download`
5. Place downloaded `sab_postProcess.py` in SABnzbd's scripts directory and mark executable.
6. In SABnzbd, assign that script as the post-process script for target categories/jobs.
7. Run a test download and verify:
   - SAB script logs show matching/tag/delete decisions
   - `GET /v1/stats-summary` increments counters
   - UI stats ribbon and `Since` date update

## Root Configuration UI

Visit `http://localhost:9995/` for a web UI to manage Stash endpoints.

- Supports CRUD for multiple endpoints
- Stores entries in a local JSON file (default: `/config/config.json`)
- Validates endpoint on add/update by querying GraphQL for Stash version
- Displays entries with right-aligned phash scene counts (example: `Primary Stash    84,123`)
- Name tooltip shows version; count tooltip shows phash coverage (example: `92.00% phashes. 84,123 of 90,456 scenes`)
- Refreshes scene/phash metrics on each page load
- Refreshes endpoint version lazily when hovering endpoint name
- Settings area is a collapsible drawer (collapsed by default when endpoints exist)
- Shows a top stats ribbon (hash count, data sum, deletes, L/F/D tags, video/hash duration sums, and since date)
- Stats values use compact display formatting (for example: `1.2K`, `2.9M`, `3.1B`)
- Duration sums use compact two-unit formatting (for example: `8d 7.9h`) with full raw values in tooltips
- Includes an `📖 About` drawer between stats and settings; auto-expands on first load when no files have been hashed yet
- Includes a manual file browser + hash runner workflow:
  - defaults to `/downloads` if present, else `/`
  - shows name, size, and modified date
  - supports up-folder navigation, single-select highlight, and double-click actions
  - generates curl command for selected file
  - provides `Download SAB Script` via modal (`Cancel`, `Detect URL`, `Download`) to export a preconfigured post-process script using current configurator values (`stashIndex`, `maxTimeDelta`, `maxDistance`) and an explicit endpoint URL
  - runs hash on double-click or `Hash` button
  - displays a working spinner and JSON results panel
  - UI areas are split into drawers: `🐍 Configurator` and `🏗 Playground`
- Includes a dedicated logs page at `/logs`:
  - reverse chronological records from `record_stats`
  - paginated 100 rows per page
  - auto-refresh polling every 2 seconds
  - expandable per-row details
  - destructive clear action with warning/confirmation
- Uses `resources/logo.png` for branding
- Generates `favicon.ico` from `resources/favicon_source.png` during container build

## Easy Path (GHCR)

Pull and run the latest image:

```bash
docker pull ghcr.io/stash-kennyg/hasharr:latest
docker run --rm -p 9995:9995 \
  -v /path/to/hasharr-config:/config \
  -v /downloaded:/downloaded:ro \
  ghcr.io/stash-kennyg/hasharr:latest
```

Hello World:
```
curl -s -X GET http://localhost:9995/v1/healthz 
```

Test request:

```bash
curl -s -X POST http://localhost:9995/v1/phash \
  -H 'Content-Type: text/plain' \
  --data '"/downloaded/comp/man/vid/vid123.mp4"'
```

SAB script download example:

```bash
curl -L "http://localhost:9995/v1/sab-postprocess.py?stashIndex=-1&maxTimeDelta=1&maxDistance=0&hasharrUrl=http://hasharr:9995" -o sab_postProcess.py
chmod +x sab_postProcess.py
```

## API

### `POST /v1/phash`

Accepted request body formats:

- plain text body:
  - `"/downloaded/comp/man/vid/vid123.mp4"`
  - `/downloaded/comp/man/vid/vid123.mp4`
- JSON:
  - `{"path":"/downloaded/comp/man/vid/vid123.mp4"}`

Response:

```json
{
  "phash": "7f007f20ff20ff00",
  "resolution_x": 1280,
  "resolution_y": 720,
  "dimensions": "1280 x 720",
  "duration": 123.45,
  "bitrate": 1450.2,
  "frame_rate": 23.98
}
```

- `duration`: seconds (2 decimals)
- `bitrate`: kilobits/sec, computed as `(file_size_bytes * 8) / duration / 1000` (1 decimal)

### `POST /v1/phash-match`

Hashes a file and performs Stash lookup using a selected configured endpoint.

Body:

```json
{
  "path": "/downloaded/comp/man/vid/vid123.mp4",
  "endpointId": "ep1"
}
```

Lookup strategy:

1. Exact: same phash + duration window
2. Fallback (only when no exact): phash distance `<= 10` and duration within `min(1%, 15s)`

### `GET /healthz`

Returns:

```json
{"status":"ok"}
```

### `GET /v1/stash-endpoints`

Returns configured endpoints.

### `POST /v1/stash-endpoints`

Create and validate a new endpoint.

Body:

```json
{
  "name": "PrimaryStash",
  "graphqlUrl": "http://stash.local:9999/graphql",
  "apiKey": "optional"
}
```

### `PUT /v1/stash-endpoints/{id}`

Update and revalidate an endpoint.

### `DELETE /v1/stash-endpoints/{id}`

Delete an endpoint.

### `GET /v1/sab-postprocess.py`

Downloads a SABnzbd post-process Python script (`sab_postProcess.py`) with defaults baked in from query params:

- `stashIndex` (default `-1`)
- `maxTimeDelta` (default `1`, clamped to `0..15`)
- `maxDistance` (default `0`, clamped to `0..8`)
- `hasharrUrl` (default request origin, fallback `http://hasharr:9995`)

Example:

```bash
curl -L "http://localhost:9995/v1/sab-postprocess.py?stashIndex=-1&maxTimeDelta=1&maxDistance=0&hasharrUrl=http://hasharr:9995" -o sab_postProcess.py
chmod +x sab_postProcess.py
```

### `POST /v1/record-stats`

Stores per-file SAB post-process outcomes in SQLite.

Body:

```json
{
  "sabNzoID": "SAB_JOB_ID",
  "fileName": "example.mp4",
  "fileSizeBytes": 123456789,
  "fileDurationSeconds": 2089.28,
  "hashDurationSeconds": 2.71,
  "outcome": 7
}
```

Outcome is a bitmask (0-15):

- `8`: Deleted
- `4`: L (larger resolution)
- `2`: D (longer duration, >1s threshold)
- `1`: F (higher FPS, normalized/capped at 30)

Stored in `/config/hasharr-stats.db` (same directory as config JSON).

### `GET /v1/stats-summary`

Returns aggregated stats from the SQLite store, including:

- `hashCount`
- `dataBytesSum`
- `deleteCount`
- `lCount`
- `fCount`
- `dCount`
- `videoDurationSumSec`
- `hashDurationSumSec`
- `since` (minimum `created_at` timestamp; UI renders as ISO `YYYY-MM-DD`)

### `GET /v1/stats-logs`

Returns paginated rows from `record_stats` in reverse chronological order.

Query params:

- `page` (default `1`)
- `pageSize` (default `100`, max `100`)

Response:

```json
{
  "page": 1,
  "pageSize": 100,
  "total": 3456,
  "rows": [
    {
      "id": 3456,
      "sabNzoID": "job-123",
      "fileName": "example.mp4",
      "fileSizeBytes": 123456789,
      "fileDurationSeconds": 2089.28,
      "hashDurationSeconds": 1.22,
      "outcome": 6,
      "createdAt": "2026-04-01T12:34:56.123456Z"
    }
  ]
}
```

### `POST /v1/stats-logs/clear`

Deletes all rows from `record_stats`.

Returns:

```json
{"status":"ok"}
```

## SABnzbd post-process script

Script path in this repo:

- `resources/sab_postProcess.py`

Behavior summary:

- scans completed job folder recursively for video files
- skips files containing `.sample`
- when exactly 2 candidate videos exist, skips `.1.<ext>` split-part style files
- exact match sweep via `/v1/phash-match` using configured defaults
  - deletes source file if exact matches exist and source is not better
  - prefixes filename with quality reasons if source is better:
    - `L` = larger resolution
    - `D` = longer duration
    - `F` = higher FPS (FPS normalized with cap at 30)
- optimistic sweep when no exact match:
  - `maxDistance=8`, `maxTimeDelta=min(15, duration*0.02)`
  - prefixes with `[P]` if potential match found
- posts one `/v1/record-stats` row per processed file
- UI hash runs also post one `/v1/record-stats` row per successful run (with `sabNzoID` set to `ui` and `outcome=0`)

Exit codes:

- `0`: normal completion
- `1`: failed deleting job folder after deleting all eligible videos

## Run locally

Dependencies:

- `go 1.24+`
- `ffmpeg`, `ffprobe`

```bash
go run ./cmd/hasharr
```

Then:

```bash
curl -s -X POST http://localhost:9995/v1/phash \
  -H 'Content-Type: text/plain' \
  --data '"/path/to/video.mp4"'
```

Default listen address is `:9995` (override with `HASHARR_ADDR`).
Static asset path defaults to `./resources` (override with `HASHARR_RESOURCES_DIR`).

Branding asset routes:

- `/logo.png`
- `/favicon.ico`
- `/favicon-source.png`

For external references (Docker icon/docs), `favicon_source.png` can be linked directly from GitHub raw content.

Local icon generation:

```bash
make icons
```


## Docker

Build:

```bash
docker build -t hasharr:dev .
```

Run:

```bash
docker run --rm -p 9995:9995 \
  -v /path/to/hasharr-config:/config \
  -v /downloaded:/downloaded:ro \
  hasharr:dev
```

> The container must be able to read the target media path (bind mount your media directories).

## Test Resources

Integration test media fixtures are stored under `resources/`.

- current fixture: `resources/tests__LosAlamosPhysicalSimulations_1m.mp4`

