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

Runs the ffmpeg-backed fixture validation:

1. Checkout repository
2. Set up Go from `go.mod`
3. Install `ffmpeg`
4. Run fixture integration test with verbose output:
   - `go test -v -tags=integration -run TestComputeIntegrationFixture ./internal/phash`

Notes:

- CI intentionally runs the fixture test only (the primary compatibility check).
- The synthetic integration test remains in the codebase for local/debug validation.

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

## `bump-version-on-main-merge.yml`

Automatically bumps the minor version in `cmd/hasharr/VERSION` after PR merges to `main`.

### Trigger

- `pull_request` with `types: [closed]` on `main`
- runs only when `github.event.pull_request.merged == true`

### Behavior

1. Checks out `main`
2. Reads `cmd/hasharr/VERSION` in `X.Y` format
3. Increments minor (`X.Y -> X.(Y+1)`)
4. Commits and pushes the version file update

### Important configuration

- Uses `stefanzweifel/git-auto-commit-action@v6`
- Explicitly sets `branch: main` to avoid pushing to the PR head branch context
