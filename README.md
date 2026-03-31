# hasharr

[![CI](https://github.com/Stash-KennyG/hasharr/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Stash-KennyG/hasharr/actions/workflows/ci.yml)

`hasharr` is a container-friendly web service that computes a Stash-style video perceptual hash (`phash`) and basic media metadata from a local file path.

## Root Configuration UI

Visit `http://localhost:9995/` for a web UI to manage Stash endpoints.

- Supports CRUD for multiple endpoints
- Stores entries in a local JSON file (default: `/config/config.json`)
- Validates endpoint on add/update by querying GraphQL for Stash version
- Displays entries as `Name vVersion` (example: `PrimaryStash v0.31.0`)

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

