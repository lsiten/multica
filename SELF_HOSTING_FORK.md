# Deploying lsiten/multica

GitHub Actions builds the fork images; the server only pulls and runs them.
The upstream installer/images do not contain this fork's custom features.

## Build on GitHub

The **Fork Images** workflow runs on pushes to `main` in `lsiten/multica`, and
can be started manually on `main`. Enable Actions in the fork if disabled.
It uses `GITHUB_TOKEN` with `packages: write`; no server credentials are needed.
It does not deploy to the server, create release tags, or release desktop/CLI
artifacts. The existing release-tag workflow remains separate.

Native amd64 and arm64 runners build both application images. After all four
builds succeed, matching `sha-<full SHA>` manifests are published, then the
`main` channel is updated. Pin both services to the same SHA for reproducibility;
moving channel tags are not atomically updated.

Wait for **Fork Images** to succeed before deploying. New GHCR packages can be
private: make them public in package settings for anonymous pulls, or log the
server into GHCR using a credential with `read:packages`. Never commit registry
credentials or pass them as command-line arguments.

## Deploy or update

Back up the database first. Update the server checkout and explicitly update
the existing `.env` (old values still override defaults):

```dotenv
MULTICA_BACKEND_IMAGE=ghcr.io/lsiten/multica-backend
MULTICA_WEB_IMAGE=ghcr.io/lsiten/multica-web
MULTICA_IMAGE_TAG=main
FRONTEND_ORIGIN=https://multica.lene.fun
MULTICA_APP_URL=https://multica.lene.fun
MULTICA_PUBLIC_URL=https://multica.lene.fun
```

The public deployment is `https://multica.lene.fun`. Keep the web container's
`REMOTE_API_URL=http://backend:8080` for internal requests; do not point it back
at the public reverse proxy. Route `/api/`, `/auth/`, and `/ws` (with WebSocket
upgrade) to the backend, and page requests to the web container. Keep existing
working reverse-proxy rules when upgrading. No desktop artifacts are published.

Replace `main` with `sha-<full successful build commit>` to pin a version. Old
`multica-ai` image names and `latest` tags must be replaced. Then run:

```bash
make selfhost
```

This pulls both application images before updating containers, and runs pending
migrations before starting the backend. Pull failures stop deployment without
falling back to compilation. Existing `.env` and data volumes are retained.
This command does not pull Git changes for you. `make selfhost-images` is equivalent.

With an already configured `.env`, the equivalent direct Compose entry is:

```bash
docker compose -f docker-compose.selfhost.yml pull
docker compose -f docker-compose.selfhost.yml up -d --no-build
```

The application services use prebuilt images; no compilation runs on the server.
The backend image includes `server/migrations`, including notification-bot
migrations 451–454; startup aborts if a migration fails.

## Notification bots

Set `MULTICA_NOTIFICATION_SECRET_KEY` in the server's `.env` before deployment.
Generate it once with `openssl rand -base64 32`, keep it private and back it up.
Do not replace an existing key: saved bot credentials depend on it. Without this
key the app can start, but notification bots remain unavailable. Configure
`MULTICA_APP_URL` with the public HTTPS URL for links in notifications.

## Optional local build

```bash
make selfhost-build
```

Only use this on a machine with sufficient build resources. It uses the explicit
`docker-compose.selfhost.build.yml` override. Normal deployment never compiles.

Do not use `down -v` during an upgrade: it deletes the database/upload volumes.
Inspect startup failures with:

```bash
docker compose -f docker-compose.selfhost.yml logs --tail=150 backend
```
