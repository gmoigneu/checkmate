# Checkmate

Checkmate is a self-hosted personal task manager. It combines a TanStack
frontend, a Go API, and a Model Context Protocol (MCP) endpoint in one
deployable binary backed by SQLite.

## Repository layout

- `web/` — React and TanStack frontend.
- `server/` — Go API, operational CLI, embedded migrations, and embedded web
  application.
- `specs/` — OpenAPI specification.
- `server/portainer-stack.yml` — production deployment for Portainer.

The frontend build is copied into the Go server and embedded at compile time.
The resulting `checkmate` binary serves the UI, API, OAuth endpoints, and MCP
endpoint on the same port.

## Local development

Requirements:

- Go 1.26.4 or newer.
- Node.js 24 and npm.
- GNU Make.

Build the frontend and server:

```sh
cd server
make build
```

Create the first account and start Checkmate:

```sh
./bin/checkmate user create \
  -email you@example.com \
  -name "Your Name" \
  -timezone Europe/Paris \
  -token "local"

./bin/checkmate serve
```

Checkmate is then available at <http://localhost:8080>. The account command
prints the API token once; only its SHA-256 hash is stored.

Run the server tests and frontend checks with:

```sh
cd server
make test

cd ../web
npm run check
```

See [`server/README.md`](server/README.md) for configuration, API, OAuth, MCP,
data model, and fixture documentation.

## Docker

The root [`Dockerfile`](Dockerfile) builds the frontend, embeds it in the Go
binary, and creates a non-root Alpine image with CA certificates and timezone
data. SQLite data is stored in `/data`.

Build an amd64 image into the local Docker image store:

```sh
docker buildx build \
  --platform linux/amd64 \
  --build-arg VERSION="$(git describe --tags --always --dirty)" \
  --tag checkmate:local \
  --load \
  .
```

Run it locally:

```sh
docker run --rm \
  --publish 8080:8080 \
  --volume checkmate-data:/data \
  --env CHECKMATE_ENV=development \
  --env CHECKMATE_BASE_URL=http://localhost:8080 \
  checkmate:local
```

### Publish to GitHub Container Registry

Create a GitHub personal access token with `write:packages`, then authenticate:

```sh
export GHCR_TOKEN="your-token"
printf '%s' "$GHCR_TOKEN" |
  docker login ghcr.io --username gmoigneu --password-stdin
```

Build and push immutable and `latest` amd64 tags:

```sh
IMAGE=ghcr.io/gmoigneu/checkmate
VERSION="$(git describe --tags --always)"

docker buildx build \
  --platform linux/amd64 \
  --build-arg VERSION="$VERSION" \
  --tag "$IMAGE:$VERSION" \
  --tag "$IMAGE:latest" \
  --push \
  .
```

Confirm the published platform:

```sh
docker buildx imagetools inspect ghcr.io/gmoigneu/checkmate:latest
```

Deploy [`server/portainer-stack.yml`](server/portainer-stack.yml) in Portainer
after publishing the image. At minimum, set `CHECKMATE_BASE_URL` to the public
HTTPS origin. Google sign-in additionally requires
`CHECKMATE_GOOGLE_CLIENT_ID` and `CHECKMATE_GOOGLE_CLIENT_SECRET`.
