# Tool to search for and select a movie
## Objectives
 - Easily search and find movies
 - Select multiple at a time
 - Notify maintainer/ai agent to be torrented or ripped from blu-ray
 - Look nice with large movie thumbnails from search results
 - Output movies titles/other identifying info in a standard format
 - Easy to use management mode which allows movies to be marked as in progress or completed
    - Api that agent can use to automatically query and use to download media and manage movies
 - Very lightweight so I don't have to worry about resource use
## What this is
A simple website that allows users to search for movies and shows to add to a queue
which will be saved on a database like sqlite to be downloaded to a nas by a maintainer
or an ai agent. Will primarily be ran inside of a docker container to be used with TrueNAS

## How it works

A single Go binary (~13 MB, ~15 MB RAM, no CGO) that serves the website, the JSON
API and runs the daily email job. Data lives in one SQLite file.

| Objective | Where |
|---|---|
| Search movies and shows | `/` — searches [TMDB](https://www.themoviedb.org/); with no key, a built-in demo catalog of 30 titles |
| Select multiple | Click posters to select, then **Request** in the tray at the bottom |
| Notify maintainer / agent | Daily email digest of new requests (`NOTIFY_AT`), plus the API |
| Large thumbnails | Poster grid, overview on hover, queue status badges |
| Standard format | Every request has `folder_name`, e.g. `The Matrix (1999) {tmdb-603}` (Jellyfin/Plex naming), plus IMDb and TMDB IDs. Export as txt/CSV/JSON |
| Management mode | `/manage` (admin token): start / complete / fail / requeue, source, notes, delete, export |
| Agent API | `GET /api/queue`, `POST /api/queue/claim`, `PATCH /api/requests/{id}`. See [API.md](API.md) |

## Quick start (dev)

```sh
nix develop          # go, gopls, sqlite, mailpit, curl, jq
cp .env.example .env # then set ADMIN_TOKEN
make mail            # terminal 1: fake SMTP server, inbox at http://localhost:8025
make dev             # terminal 2: http://localhost:8088 (LISTEN_ADDR in .env)
```

Then:

1. Open http://localhost:8088, click a few posters, enter a name and press **Request**.
2. Open http://localhost:8088/manage, enter your `ADMIN_TOKEN`, and work the queue.
3. Press **Send digest now** (or wait for `NOTIFY_AT`), and the email shows up at http://localhost:8025.
4. Try the API: `curl -s localhost:8088/api/queue | jq` or `curl localhost:8088/api/queue?format=txt`.

For real search, get a free TMDB key at https://www.themoviedb.org/settings/api and set
`TMDB_API_KEY` (the v3 API key and the v4 read access token both work).

`make test` runs the test suite.

## Configuration

All configuration is environment variables (`make dev` loads `.env`):

| Variable | Default | |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | |
| `DB_PATH` | `data/movies.db` | SQLite file; directory is created |
| `ADMIN_TOKEN` | random each start (printed to the log) | Management mode and admin API |
| `TMDB_API_KEY` | none (demo catalog) | |
| `BASE_URL` | `http://localhost:8080` | Used for links in emails |
| `NOTIFY_EMAIL` | none | Comma-separated recipients |
| `NOTIFY_AT` | `09:00` | Local time for the daily digest; `off` disables it |
| `TZ` | UTC | e.g. `America/New_York` |
| `SMTP_HOST` / `SMTP_PORT` | none / `587` | |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | none | For Gmail, use an [app password](https://myaccount.google.com/apppasswords) |
| `SMTP_FROM` | `movieselector@localhost` | |
| `SMTP_TLS` | auto | `tls` (port 465), `starttls`, `none`, or auto |

**Daily digest.** Once a day at `NOTIFY_AT`, every request not yet included in a digest is
emailed in one message; nothing is sent on days without new requests. The last send date is
stored in the database, so restarts don't send twice, and a digest missed while the server was
down goes out when it starts again. Without SMTP configured the digest is printed to the log.

**Security.** Anyone who can reach the site can search, request and view the queue; changes need
the admin token. Run it on your LAN or behind a VPN or reverse proxy. Don't expose it to the internet as it is.

## Deploying (Docker / TrueNAS)

```sh
docker build -t movieselector .   # ~20 MB image, from scratch, runs as uid 568 (TrueNAS "apps")
docker run -d -p 8080:8080 -v /mnt/tank/apps/movieselector:/data \
  -e ADMIN_TOKEN=... -e TMDB_API_KEY=... -e NOTIFY_EMAIL=... -e SMTP_HOST=... \
  movieselector
```

See `docker-compose.yml` for a full example (TrueNAS SCALE: Apps → Discover → Custom App → Install via YAML).
The `/data` dataset must be writable by uid 568.

Nix users can also `nix build` (output: `result/bin/movieselector`). After changing `go.mod`,
update `vendorHash` in `flake.nix` (set it to `pkgs.lib.fakeHash`, build, and copy the hash from the error).

## Layout

```
main.go     config + wiring          api.go      HTTP routes and handlers
store.go    SQLite queue             notify.go   digest email + daily scheduler
tmdb.go     TMDB search provider     demo.go     offline demo catalog + generated posters
format.go   standard folder names    web/        embedded frontend (vanilla JS, no build step)
```
