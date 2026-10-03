# movieSelector API

Base URL: wherever the server runs, e.g. `http://nas.local:8080`. All bodies are JSON.

**Auth.** Reading (search, queue, single request) and creating requests are open to anyone
on the network. Everything that changes the queue needs the admin token:

```
Authorization: Bearer $ADMIN_TOKEN
```

Errors look like `{"error": "message"}` with a 4xx/5xx status.

## The request object

```json
{
  "id": 1,
  "media_type": "movie",              // "movie" | "tv"
  "tmdb_id": 603,
  "imdb_id": "tt0133093",             // may be "" if unknown
  "title": "The Matrix",
  "year": 1999,
  "overview": "...",
  "poster_url": "https://image.tmdb.org/t/p/w500/....jpg",
  "status": "queued",                 // queued | in_progress | completed | failed
  "source": "",                       // free text; UI uses torrent | bluray | other
  "assignee": "",                     // who claimed it, e.g. "agent-1"
  "note": "",
  "requested_by": "Sam",              // first requester
  "request_count": 2,                 // how many times it was requested
  "created_at": "2026-10-02T21:00:00Z",
  "updated_at": "2026-10-02T21:05:00Z",
  "notified_at": null,                // when it went out in an email digest
  "display_name": "The Matrix (1999)",
  "folder_name": "The Matrix (1999) {tmdb-603}"
}
```

`folder_name` is the standard identifier: the Jellyfin / Plex / Emby naming convention,
with filesystem-illegal characters removed. Use it as the directory name on the NAS and
the media server will match it exactly.

## Endpoints

### Public

| Method & path | Purpose |
|---|---|
| `GET /api/health` | `{"ok": true, "provider": "tmdb"\|"demo", "email_enabled": bool}` |
| `GET /api/search?q=&type=&page=` | Search. `type` = `movie`, `tv` or empty for both. Empty `q` returns trending. Each result carries `status` if already requested. |
| `POST /api/requests` | Add titles to the queue (see below). |
| `GET /api/queue` | The queue (see below). |
| `GET /api/requests/{id}` | One request. |

### Admin (token required)

| Method & path | Purpose |
|---|---|
| `POST /api/queue/claim` | Atomically take the next queued title and mark it `in_progress`. |
| `PATCH /api/requests/{id}` | Change `status`, `source`, `assignee` and/or `note`. |
| `DELETE /api/requests/{id}` | Remove a request. `204` on success. |
| `GET /api/digest/preview` | Show what the next email digest would contain. `?format=html` renders the email. |
| `POST /api/digest/send` | Send the digest now (normally sent once a day at `NOTIFY_AT`). |
| `GET /api/auth` | `200` if the token is valid, `401` otherwise. |

### `POST /api/requests`

```json
{"requested_by": "Sam", "items": [{"media_type": "movie", "tmdb_id": 603}, {"media_type": "tv", "tmdb_id": 1396}]}
```

Only IDs are sent; the server looks up the metadata itself. Up to 50 items. Response
(`201` if anything was added, else `200`):

```json
{"added": [<request>...], "existing": [<request>...], "errors": [{"media_type": "movie", "tmdb_id": 1, "error": "title not found"}]}
```

Requesting a title that is already in the queue increments its `request_count`. Requesting
a `failed` title puts it back to `queued`. Completed titles are left alone.

### `GET /api/queue`

| Param | Values | Default |
|---|---|---|
| `status` | comma-separated statuses, or `all` | `queued,in_progress` |
| `type` | `movie` or `tv` | both |
| `format` | `json`, `txt` (one `folder_name` per line), `csv` | `json` |

JSON response: `{"items": [<request>...], "counts": {"queued": 2, "in_progress": 1, "completed": 5, "failed": 0}}`.
Items are oldest first.

### `POST /api/queue/claim`

```json
{"assignee": "agent-1", "source": "torrent", "media_type": "movie"}
```

All fields optional. Picks the most-requested queued title (oldest first on ties),
sets it to `in_progress` and returns it. Returns `204 No Content` when nothing is queued.
Safe to call from several workers at once.

### `PATCH /api/requests/{id}`

```json
{"status": "completed", "note": "1080p remux, 24 GB"}
```

Any subset of `status`, `source`, `assignee`, `note`. Unknown fields are rejected.

## Agent workflow example

```sh
B=http://nas.local:8080; T="Authorization: Bearer $ADMIN_TOKEN"

# take the next job
job=$(curl -sf -XPOST -H "$T" $B/api/queue/claim -d '{"assignee":"agent-1","source":"torrent"}') || exit 0
id=$(echo "$job" | jq -r .id)
dir=$(echo "$job" | jq -r .folder_name)
imdb=$(echo "$job" | jq -r .imdb_id)

# ... download into "/mnt/media/movies/$dir" using $imdb to find the release ...

# report back
curl -s -XPATCH -H "$T" $B/api/requests/$id -d '{"status":"completed"}'
# or: -d '{"status":"failed","note":"no release found"}'
```
