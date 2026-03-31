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
  "duration": 123.45,
  "bitrate": 1450.2
}
```

- `duration`: seconds (2 decimals)
- `bitrate`: kilobits/sec, computed as `(file_size_bytes * 8) / duration / 1000` (1 decimal)

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

