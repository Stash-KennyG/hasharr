# hasharr

[![CI](https://github.com/Stash-KennyG/hasharr/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Stash-KennyG/hasharr/actions/workflows/ci.yml)

`hasharr` is a container-friendly web service that computes a Stash-style video perceptual hash (`phash`) and basic media metadata from a local file path.

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
- Includes a manual file browser + hash runner workflow:
  - defaults to `/downloaded` if present, else `/`
  - shows name, size, and modified date
  - supports up-folder navigation, single-select highlight, and double-click actions
  - generates curl command for selected file
  - provides `Download SAB Script` to export a preconfigured post-process script using current configurator values (`stashIndex`, `maxTimeDelta`, `maxDistance`)
  - runs hash on double-click or `Hash` button
  - displays a working spinner and JSON results panel
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

Example:

```bash
curl -L "http://localhost:9995/v1/sab-postprocess.py?stashIndex=-1&maxTimeDelta=1&maxDistance=0" -o sab_postProcess.py
chmod +x sab_postProcess.py
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

Exit codes:

- `0`: normal completion
- `1`: all eligible videos deleted (script also attempts to remove job folder)
- `2`: potential-only outcomes

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

