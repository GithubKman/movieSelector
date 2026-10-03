package main

import (
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

type Server struct {
	store      *Store
	provider   Provider
	notifier   *Notifier
	adminToken string
}

func NewServer(store *Store, provider Provider, notifier *Notifier, adminToken string) *Server {
	return &Server{store: store, provider: provider, notifier: notifier, adminToken: adminToken}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", serveFile(static, "index.html"))
	mux.HandleFunc("GET /manage", serveFile(static, "manage.html"))
	if demo, ok := s.provider.(*DemoProvider); ok {
		mux.HandleFunc("GET /demo-poster/{type}/{id}", demo.ServePoster)
	}

	// Public: anyone on the network can search, request and see the queue.
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("POST /api/requests", s.createRequests)
	mux.HandleFunc("GET /api/queue", s.queue)
	mux.HandleFunc("GET /api/requests/{id}", s.getRequest)

	// Admin: management mode and agents. Require the admin token.
	mux.HandleFunc("PATCH /api/requests/{id}", s.admin(s.updateRequest))
	mux.HandleFunc("DELETE /api/requests/{id}", s.admin(s.deleteRequest))
	mux.HandleFunc("POST /api/queue/claim", s.admin(s.claim))
	mux.HandleFunc("GET /api/digest/preview", s.admin(s.digestPreview))
	mux.HandleFunc("POST /api/digest/send", s.admin(s.digestSend))
	mux.HandleFunc("GET /api/auth", s.admin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	return logRequests(mux)
}

func serveFile(fsys fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, fsys, name)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

// admin wraps a handler to require `Authorization: Bearer <ADMIN_TOKEN>`.
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
	}
	return id, err == nil
}

func validMediaType(t string) bool { return t == "movie" || t == "tv" }

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"provider":      s.provider.Name(),
		"email_enabled": s.notifier.Enabled(),
	})
}

// GET /api/search?q=matrix&type=movie|tv&page=1
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	mt := r.URL.Query().Get("type")
	if mt != "" && !validMediaType(mt) {
		writeError(w, http.StatusBadRequest, "type must be movie or tv")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	res, err := s.provider.Search(r.Context(), q, mt, page)
	if err != nil {
		log.Printf("search %q: %v", q, err)
		writeError(w, http.StatusBadGateway, "search provider error")
		return
	}
	statuses, err := s.store.StatusMap(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	for i := range res.Results {
		t := &res.Results[i]
		t.Status = statuses[fmt.Sprintf("%s:%d", t.MediaType, t.TMDBID)]
	}
	writeJSON(w, http.StatusOK, res)
}

type requestItem struct {
	MediaType string `json:"media_type"`
	TMDBID    int    `json:"tmdb_id"`
}

type itemError struct {
	requestItem
	Error string `json:"error"`
}

// POST /api/requests {"requested_by": "Sam", "items": [{"media_type": "movie", "tmdb_id": 603}]}
// Metadata is looked up server-side from the provider, so clients only send IDs.
func (s *Server) createRequests(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestedBy string        `json:"requested_by"`
		Items       []requestItem `json:"items"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Items) == 0 || len(body.Items) > 50 {
		writeError(w, http.StatusBadRequest, "items must contain 1-50 titles")
		return
	}
	by := strings.TrimSpace(body.RequestedBy)
	if len(by) > 64 {
		by = by[:64]
	}

	out := struct {
		Added    []*Request  `json:"added"`
		Existing []*Request  `json:"existing"`
		Errors   []itemError `json:"errors"`
	}{[]*Request{}, []*Request{}, []itemError{}}

	for _, it := range body.Items {
		if !validMediaType(it.MediaType) || it.TMDBID <= 0 {
			out.Errors = append(out.Errors, itemError{it, "invalid media_type or tmdb_id"})
			continue
		}
		title, err := s.provider.Details(r.Context(), it.MediaType, it.TMDBID)
		if err != nil {
			msg := "lookup failed"
			if errors.Is(err, ErrNotFound) {
				msg = "title not found"
			}
			log.Printf("details %s/%d: %v", it.MediaType, it.TMDBID, err)
			out.Errors = append(out.Errors, itemError{it, msg})
			continue
		}
		req, created, err := s.store.AddRequest(r.Context(), *title, by)
		if err != nil {
			serverError(w, err)
			return
		}
		if created {
			out.Added = append(out.Added, req)
		} else {
			out.Existing = append(out.Existing, req)
		}
	}
	code := http.StatusOK
	if len(out.Added) > 0 {
		code = http.StatusCreated
	}
	writeJSON(w, code, out)
}

// GET /api/queue?status=queued,in_progress&type=movie&format=json|txt|csv
// status defaults to "queued,in_progress"; use status=all for everything.
func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	statuses := []string{StatusQueued, StatusInProgress}
	if v := q.Get("status"); v == "all" {
		statuses = nil
	} else if v != "" {
		statuses = strings.Split(v, ",")
		for _, st := range statuses {
			if !validStatuses[st] {
				writeError(w, http.StatusBadRequest, ErrBadStatus.Error())
				return
			}
		}
	}
	reqs, err := s.store.List(r.Context(), statuses)
	if err != nil {
		serverError(w, err)
		return
	}
	if mt := q.Get("type"); mt != "" {
		filtered := reqs[:0]
		for _, req := range reqs {
			if req.MediaType == mt {
				filtered = append(filtered, req)
			}
		}
		reqs = filtered
	}

	switch q.Get("format") {
	case "txt":
		// One Jellyfin/Plex folder name per line, e.g. "The Matrix (1999) {tmdb-603}".
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, req := range reqs {
			fmt.Fprintln(w, req.FolderName)
		}
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="queue.csv"`)
		cw := csv.NewWriter(w)
		cw.Write([]string{"id", "media_type", "title", "year", "tmdb_id", "imdb_id", "folder_name", "status", "source", "assignee", "requested_by", "request_count", "created_at", "note"})
		for _, req := range reqs {
			cw.Write([]string{
				strconv.FormatInt(req.ID, 10), req.MediaType, req.Title, strconv.Itoa(req.Year),
				strconv.Itoa(req.TMDBID), req.IMDBID, req.FolderName, req.Status, req.Source, req.Assignee,
				req.RequestedBy, strconv.Itoa(req.RequestCount), req.CreatedAt.Format(time.RFC3339), req.Note,
			})
		}
		cw.Flush()
	case "", "json":
		counts, err := s.store.Counts(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": reqs, "counts": counts})
	default:
		writeError(w, http.StatusBadRequest, "format must be json, txt or csv")
	}
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	req, err := s.store.Get(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	if req == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// PATCH /api/requests/{id} {"status": "in_progress", "source": "bluray", "assignee": "me", "note": "..."}
func (s *Server) updateRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var u Update
	if !decodeJSON(w, r, &u) {
		return
	}
	req, err := s.store.Update(r.Context(), id, u)
	if errors.Is(err, ErrBadStatus) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if req == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	deleted, err := s.store.Delete(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/queue/claim {"assignee": "agent-1", "source": "torrent", "media_type": "movie"}
// Atomically takes the next queued title (most-requested first, then oldest)
// and marks it in_progress. 204 No Content when the queue is empty.
func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Assignee  string `json:"assignee"`
		Source    string `json:"source"`
		MediaType string `json:"media_type"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	if body.MediaType != "" && !validMediaType(body.MediaType) {
		writeError(w, http.StatusBadRequest, "media_type must be movie or tv")
		return
	}
	req, err := s.store.Claim(r.Context(), body.Assignee, body.Source, body.MediaType)
	if err != nil {
		serverError(w, err)
		return
	}
	if req == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) digestPreview(w http.ResponseWriter, r *http.Request) {
	d, err := s.notifier.BuildDigest(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	if d == nil {
		writeJSON(w, http.StatusOK, map[string]any{"pending": 0})
		return
	}
	if r.URL.Query().Get("format") == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, d.HTML)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": len(d.Requests), "subject": d.Subject, "text": d.Text})
}

func (s *Server) digestSend(w http.ResponseWriter, r *http.Request) {
	n, err := s.notifier.SendDigest(r.Context())
	if err != nil {
		log.Printf("digest send: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": n, "emailed": s.notifier.Enabled() && n > 0})
}
