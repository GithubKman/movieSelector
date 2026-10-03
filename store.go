package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Request statuses.
const (
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

var validStatuses = map[string]bool{
	StatusQueued: true, StatusInProgress: true, StatusCompleted: true, StatusFailed: true,
}

// Request is one queued title. JSON field names are the public API contract.
type Request struct {
	ID           int64      `json:"id"`
	MediaType    string     `json:"media_type"`
	TMDBID       int        `json:"tmdb_id"`
	IMDBID       string     `json:"imdb_id"`
	Title        string     `json:"title"`
	Year         int        `json:"year"`
	Overview     string     `json:"overview"`
	PosterURL    string     `json:"poster_url"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`   // how it is being acquired, e.g. "torrent" or "bluray"
	Assignee     string     `json:"assignee"` // who/what claimed it, e.g. "agent-1"
	Note         string     `json:"note"`
	RequestedBy  string     `json:"requested_by"`
	RequestCount int        `json:"request_count"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	NotifiedAt   *time.Time `json:"notified_at"`

	// Derived, standardised identifiers (see format.go).
	DisplayName string `json:"display_name"`
	FolderName  string `json:"folder_name"`
}

const schema = `
CREATE TABLE IF NOT EXISTS requests (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	media_type    TEXT    NOT NULL CHECK (media_type IN ('movie','tv')),
	tmdb_id       INTEGER NOT NULL,
	imdb_id       TEXT    NOT NULL DEFAULT '',
	title         TEXT    NOT NULL,
	year          INTEGER NOT NULL DEFAULT 0,
	overview      TEXT    NOT NULL DEFAULT '',
	poster_url    TEXT    NOT NULL DEFAULT '',
	status        TEXT    NOT NULL DEFAULT 'queued',
	source        TEXT    NOT NULL DEFAULT '',
	assignee      TEXT    NOT NULL DEFAULT '',
	note          TEXT    NOT NULL DEFAULT '',
	requested_by  TEXT    NOT NULL DEFAULT '',
	request_count INTEGER NOT NULL DEFAULT 1,
	created_at    TEXT    NOT NULL,
	updated_at    TEXT    NOT NULL,
	notified_at   TEXT,
	UNIQUE (media_type, tmdb_id)
);
CREATE INDEX IF NOT EXISTS requests_status ON requests (status, created_at);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

const requestCols = `id, media_type, tmdb_id, imdb_id, title, year, overview, poster_url, status, source,
	assignee, note, requested_by, request_count, created_at, updated_at, notified_at`

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if path == ":memory:" {
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection keeps SQLite simple (no SQLITE_BUSY between our own
	// goroutines) and is plenty for a household-sized queue.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

type scanner interface{ Scan(...any) error }

func scanRequest(row scanner) (*Request, error) {
	var r Request
	var created, updated string
	var notified sql.NullString
	err := row.Scan(&r.ID, &r.MediaType, &r.TMDBID, &r.IMDBID, &r.Title, &r.Year, &r.Overview,
		&r.PosterURL, &r.Status, &r.Source, &r.Assignee, &r.Note, &r.RequestedBy, &r.RequestCount,
		&created, &updated, &notified)
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if notified.Valid {
		t, _ := time.Parse(time.RFC3339, notified.String)
		r.NotifiedAt = &t
	}
	r.DisplayName = DisplayName(r.Title, r.Year)
	r.FolderName = FolderName(r.Title, r.Year, r.TMDBID)
	return &r, nil
}

func (s *Store) queryRequests(ctx context.Context, q string, args ...any) ([]*Request, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddRequest inserts a title into the queue. If it is already present, the
// existing row is returned with created=false and its request_count bumped
// (unless it is already completed). A failed request is put back in the queue.
func (s *Store) AddRequest(ctx context.Context, t Title, requestedBy string) (r *Request, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	ts := now()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO requests (media_type, tmdb_id, imdb_id, title, year, overview, poster_url, requested_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (media_type, tmdb_id) DO NOTHING`,
		t.MediaType, t.TMDBID, t.IMDBID, t.Title, t.Year, t.Overview, t.PosterURL, requestedBy, ts, ts)
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	created = n == 1
	if !created {
		_, err = tx.ExecContext(ctx, `
			UPDATE requests SET
				request_count = request_count + 1,
				status = CASE WHEN status = 'failed' THEN 'queued' ELSE status END,
				updated_at = ?
			WHERE media_type = ? AND tmdb_id = ? AND status != 'completed'`,
			ts, t.MediaType, t.TMDBID)
		if err != nil {
			return nil, false, err
		}
	}
	r, err = scanRequest(tx.QueryRowContext(ctx,
		`SELECT `+requestCols+` FROM requests WHERE media_type = ? AND tmdb_id = ?`, t.MediaType, t.TMDBID))
	if err != nil {
		return nil, false, err
	}
	return r, created, tx.Commit()
}

func (s *Store) Get(ctx context.Context, id int64) (*Request, error) {
	r, err := scanRequest(s.db.QueryRowContext(ctx, `SELECT `+requestCols+` FROM requests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// List returns requests filtered by status (any of; empty = all), oldest first.
func (s *Store) List(ctx context.Context, statuses []string) ([]*Request, error) {
	q := `SELECT ` + requestCols + ` FROM requests`
	var args []any
	if len(statuses) > 0 {
		q += ` WHERE status IN (?` + strings.Repeat(",?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	q += ` ORDER BY created_at, id`
	return s.queryRequests(ctx, q, args...)
}

// StatusMap returns the queue status of every requested title, keyed by "movie:603".
func (s *Store) StatusMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT media_type, tmdb_id, status FROM requests`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var mt, st string
		var id int
		if err := rows.Scan(&mt, &id, &st); err != nil {
			return nil, err
		}
		m[fmt.Sprintf("%s:%d", mt, id)] = st
	}
	return m, rows.Err()
}

func (s *Store) Counts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM requests GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{StatusQueued: 0, StatusInProgress: 0, StatusCompleted: 0, StatusFailed: 0}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		m[st] = n
	}
	return m, rows.Err()
}

// Update holds optional field changes; nil means "leave as is".
type Update struct {
	Status   *string `json:"status"`
	Source   *string `json:"source"`
	Assignee *string `json:"assignee"`
	Note     *string `json:"note"`
}

var ErrBadStatus = errors.New("status must be one of: queued, in_progress, completed, failed")

func (s *Store) Update(ctx context.Context, id int64, u Update) (*Request, error) {
	var sets []string
	var args []any
	if u.Status != nil {
		if !validStatuses[*u.Status] {
			return nil, ErrBadStatus
		}
		sets, args = append(sets, "status = ?"), append(args, *u.Status)
	}
	if u.Source != nil {
		sets, args = append(sets, "source = ?"), append(args, *u.Source)
	}
	if u.Assignee != nil {
		sets, args = append(sets, "assignee = ?"), append(args, *u.Assignee)
	}
	if u.Note != nil {
		sets, args = append(sets, "note = ?"), append(args, *u.Note)
	}
	if len(sets) > 0 {
		sets, args = append(sets, "updated_at = ?"), append(args, now())
		args = append(args, id)
		if _, err := s.db.ExecContext(ctx, `UPDATE requests SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Claim atomically moves the oldest queued request to in_progress and returns
// it, so several agents can work the queue without grabbing the same title.
// Returns nil when the queue is empty.
func (s *Store) Claim(ctx context.Context, assignee, source, mediaType string) (*Request, error) {
	q := `UPDATE requests SET status = 'in_progress', assignee = ?, source = CASE WHEN ? != '' THEN ? ELSE source END, updated_at = ?
		WHERE id = (SELECT id FROM requests WHERE status = 'queued'`
	args := []any{assignee, source, source, now()}
	if mediaType != "" {
		q += ` AND media_type = ?`
		args = append(args, mediaType)
	}
	q += ` ORDER BY request_count DESC, created_at, id LIMIT 1)
		RETURNING ` + requestCols
	r, err := scanRequest(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// Unnotified returns requests that have not yet been included in a digest.
func (s *Store) Unnotified(ctx context.Context) ([]*Request, error) {
	return s.queryRequests(ctx, `SELECT `+requestCols+` FROM requests WHERE notified_at IS NULL ORDER BY created_at, id`)
}

func (s *Store) MarkNotified(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{now()}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE requests SET notified_at = ? WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	return err
}

func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
