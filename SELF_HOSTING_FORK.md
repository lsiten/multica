# Deploying lsiten/multica

This fork builds the checked-out source by default. The upstream installer and
upstream prebuilt images do not contain this fork's custom features. Use a full
checkout (including Dockerfiles, apps, packages and server), not only a copied
Compose file. Docker with the Compose plugin and sufficient build resources are
required; no host Go or Node installation is needed.

## Deploy or update

Back up the database first. Update your checkout to the desired `lsiten/multica`
commit, then run from its root:

```bash
make selfhost
```

This builds both application images, recreates changed containers, and runs
database migrations before starting the backend. Existing `.env` and named data
volumes are retained. `make selfhost-build` remains an explicit source-build entry.
This command does not pull Git changes for you.

With an already configured `.env`, the equivalent direct Compose entry is:

```bash
docker compose -f docker-compose.selfhost.yml up -d
```

The application services use `pull_policy: build`, so this also rebuilds from
source rather than reusing an old upstream image. Build cache is still usable.
The backend image includes `server/migrations`, including notification-bot
migrations 451–454; startup aborts if a migration fails.

## Notification bots

Set `MULTICA_NOTIFICATION_SECRET_KEY` in the server's `.env` before deployment.
Generate it once with `openssl rand -base64 32`, keep it private and back it up.
Do not replace an existing key: saved bot credentials depend on it. Without this
key the app can start, but notification bots remain unavailable. Configure
`MULTICA_APP_URL` with the public HTTPS URL for links in notifications.

## Explicit prebuilt-image mode

```bash
make selfhost-images
```

This opts into the upstream GHCR images, or the prebuilt image repositories and
tag configured in `.env`. Only choose images known to contain the features you
need. The equivalent Compose files are `docker-compose.selfhost.yml` plus
`docker-compose.selfhost.images.yml`, with `up -d --no-build`.

Do not use `down -v` during an upgrade: it deletes the database/upload volumes.
Inspect startup failures with:

```bash
docker compose -f docker-compose.selfhost.yml logs --tail=150 backend
```
