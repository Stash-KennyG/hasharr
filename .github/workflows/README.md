# Workflows

This directory contains GitHub Actions workflows for `hasharr`.

## `ci.yml`

Primary continuous integration and container build/publish workflow.

### Triggers

- `push` to `main`
- `pull_request` to any branch

### Global settings

- Uses Node 24 for JavaScript-based actions via:
  - `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: true`
- Workflow permissions:
  - `contents: read`
  - `packages: write`

### Jobs

#### `validate-test`

Runs Go project quality, lint, and unit tests:

1. Checkout repository
2. Set up Go from `go.mod`
3. Verify formatting (`gofmt -l .`)
4. Validate module state (`go mod tidy && git diff --exit-code`)
5. Run static analysis (`go vet ./...`)
6. Run unit tests (`go test ./...`)

#### `integration-test`

Runs the ffmpeg-backed integration test suite:

1. Checkout repository
2. Set up Go from `go.mod`
3. Install `ffmpeg`
4. Run integration tests (`go test -tags=integration ./internal/phash`)

Both test jobs must pass before image build/publish.

#### `build-ghcr`

Builds the container image and conditionally publishes to GHCR.
Depends on `validate-test` and `integration-test`.

1. Checkout repository
2. Normalize image name to lowercase (`ghcr.io/<owner>/<repo>`)
3. Set up Docker Buildx
4. Login to GHCR (only on push to `main`)
5. Build image for all events
6. Push image only on push to `main`

Published tags on `main` pushes:

- `ghcr.io/<owner>/<repo>:latest`
- `ghcr.io/<owner>/<repo>:<git-sha>`

### Notes

- Pull request runs validate buildability but do not publish images.
- Lowercasing the image name avoids GHCR tag failures when owner/repo contains uppercase characters.
