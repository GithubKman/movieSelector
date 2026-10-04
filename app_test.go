package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testToken = "secret"

type testEnv struct {
	t      *testing.T
	srv    *httptest.Server
	store  *Store
	notify *Notifier
	sent   [][]byte
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{t: t, store: store}
	e.notify = &Notifier{
		Store: store, BaseURL: "http://nas:8080", To: []string{"me@example.com"},
		SMTP: SMTPConfig{Host: "smtp.example.com", Port: 587, From: "ms@example.com"},
		send: func(_ SMTPConfig, _ []string, msg []byte) error { e.sent = append(e.sent, msg); return nil },
	}
	e.srv = httptest.NewServer(NewServer(store, NewDemoProvider(), e.notify, testToken).Routes())
	t.Cleanup(func() { e.srv.Close(); store.Close() })
	return e
}

func (e *testEnv) do(method, path, token string, body any) (int, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e *testEnv) request(by string, items ...requestItem) map[string][]json.RawMessage {
	e.t.Helper()
	code, body := e.do("POST", "/api/requests", "", map[string]any{"requested_by": by, "items": items})
	if code != 200 && code != 201 {
		e.t.Fatalf("POST /api/requests: %d %s", code, body)
	}
	var res map[string][]json.RawMessage
	json.Unmarshal(body, &res)
	return res
}

func (e *testEnv) queue(query string) (items []Request) {
	e.t.Helper()
	code, body := e.do("GET", "/api/queue"+query, "", nil)
	if code != 200 {
		e.t.Fatalf("GET /api/queue: %d %s", code, body)
	}
	var res struct{ Items []Request }
	json.Unmarshal(body, &res)
	return res.Items
}

var (
	matrix = requestItem{"movie", 603}
	dune   = requestItem{"movie", 438631}
	office = requestItem{"tv", 2316}
)

func TestSearchAnnotatesStatus(t *testing.T) {
	e := newTestEnv(t)
	e.request("sam", matrix)
	_, body := e.do("GET", "/api/search?q=matrix", "", nil)
	var res SearchResult
	json.Unmarshal(body, &res)
	if len(res.Results) != 1 || res.Results[0].Title != "The Matrix" || res.Results[0].Status != StatusQueued {
		t.Fatalf("unexpected search result: %s", body)
	}
	if code, _ := e.do("GET", "/api/search?type=bogus", "", nil); code != 400 {
		t.Fatalf("bad type: got %d", code)
	}
}

func TestRequestFlow(t *testing.T) {
	e := newTestEnv(t)
	res := e.request("sam", matrix, office, requestItem{"movie", 999999999}, requestItem{"book", 1})
	if len(res["added"]) != 2 || len(res["errors"]) != 2 {
		t.Fatalf("unexpected result: %v", res)
	}
	// Re-requesting bumps the count instead of duplicating.
	res = e.request("alex", matrix)
	if len(res["added"]) != 0 || len(res["existing"]) != 1 {
		t.Fatalf("expected existing: %v", res)
	}
	items := e.queue("")
	if len(items) != 2 {
		t.Fatalf("want 2 queued, got %d", len(items))
	}
	m := items[0]
	if m.Title != "The Matrix" || m.RequestCount != 2 || m.IMDBID != "tt0133093" || m.RequestedBy != "sam" {
		t.Fatalf("unexpected item: %+v", m)
	}
	if m.FolderName != "The Matrix (1999) {tmdb-603}" {
		t.Fatalf("folder name: %q", m.FolderName)
	}
	if got := e.queue("?type=tv"); len(got) != 1 || got[0].MediaType != "tv" {
		t.Fatalf("type filter: %+v", got)
	}
}

func TestQueueFormats(t *testing.T) {
	e := newTestEnv(t)
	e.request("sam", matrix, dune)
	_, txt := e.do("GET", "/api/queue?format=txt", "", nil)
	if string(txt) != "The Matrix (1999) {tmdb-603}\nDune (2021) {tmdb-438631}\n" {
		t.Fatalf("txt: %q", txt)
	}
	_, csv := e.do("GET", "/api/queue?format=csv", "", nil)
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "id,media_type,title") || !strings.Contains(lines[1], "tt0133093") {
		t.Fatalf("csv: %s", csv)
	}
	if code, _ := e.do("GET", "/api/queue?status=nope", "", nil); code != 400 {
		t.Fatalf("bad status: %d", code)
	}
}

func TestAdminRequiresToken(t *testing.T) {
	e := newTestEnv(t)
	e.request("sam", matrix)
	for _, tok := range []string{"", "wrong"} {
		if code, _ := e.do("PATCH", "/api/requests/1", tok, map[string]string{"status": "completed"}); code != 401 {
			t.Fatalf("PATCH with token %q: %d", tok, code)
		}
		if code, _ := e.do("POST", "/api/queue/claim", tok, nil); code != 401 {
			t.Fatalf("claim with token %q: %d", tok, code)
		}
	}
}

func TestManageLifecycle(t *testing.T) {
	e := newTestEnv(t)
	e.request("sam", matrix, dune)
	e.request("alex", dune) // dune is now more popular, so it is claimed first

	code, body := e.do("POST", "/api/queue/claim", testToken, map[string]string{"assignee": "agent-1", "source": "torrent"})
	var r Request
	json.Unmarshal(body, &r)
	if code != 200 || r.Title != "Dune" || r.Status != StatusInProgress || r.Assignee != "agent-1" || r.Source != "torrent" {
		t.Fatalf("claim: %d %s", code, body)
	}

	code, body = e.do("PATCH", "/api/requests/"+strconv.FormatInt(r.ID, 10), testToken, map[string]string{"status": "completed", "note": "4K remux"})
	json.Unmarshal(body, &r)
	if code != 200 || r.Status != StatusCompleted || r.Note != "4K remux" || r.Source != "torrent" {
		t.Fatalf("patch: %d %s", code, body)
	}
	if code, _ := e.do("PATCH", "/api/requests/1", testToken, map[string]string{"status": "lost"}); code != 400 {
		t.Fatalf("invalid status accepted: %d", code)
	}
	if code, _ := e.do("PATCH", "/api/requests/1", testToken, map[string]string{"colour": "red"}); code != 400 {
		t.Fatalf("unknown field accepted: %d", code)
	}

	// Completed titles drop off the default queue view but stay in status=all.
	if got := e.queue(""); len(got) != 1 || got[0].Title != "The Matrix" {
		t.Fatalf("open queue: %+v", got)
	}
	if got := e.queue("?status=completed"); len(got) != 1 {
		t.Fatalf("completed: %+v", got)
	}
	// Requesting a completed title doesn't requeue it.
	if res := e.request("kim", dune); len(res["existing"]) != 1 {
		t.Fatalf("re-request completed: %v", res)
	}
	if got := e.queue("?status=completed"); got[0].RequestCount != 2 {
		t.Fatalf("completed title count changed: %d", got[0].RequestCount)
	}

	e.do("POST", "/api/queue/claim", testToken, nil) // takes the matrix
	if code, _ := e.do("POST", "/api/queue/claim", testToken, nil); code != 204 {
		t.Fatalf("empty queue claim: %d", code)
	}

	if code, _ := e.do("DELETE", "/api/requests/1", testToken, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := e.do("GET", "/api/requests/1", "", nil); code != 404 {
		t.Fatalf("get deleted: %d", code)
	}
}

func TestFailedRequestIsRequeuedOnRerequest(t *testing.T) {
	e := newTestEnv(t)
	e.request("sam", matrix)
	e.do("PATCH", "/api/requests/1", testToken, map[string]string{"status": "failed"})
	e.request("sam", matrix)
	if got := e.queue("?status=queued"); len(got) != 1 {
		t.Fatalf("failed title not requeued: %+v", got)
	}
}

func TestDigest(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	if n, err := e.notify.SendDigest(ctx); err != nil || n != 0 || len(e.sent) != 0 {
		t.Fatalf("empty digest should not send: n=%d err=%v", n, err)
	}

	e.request("sam", matrix, office)
	code, body := e.do("GET", "/api/digest/preview", testToken, nil)
	if code != 200 || !strings.Contains(string(body), "2 new titles") {
		t.Fatalf("preview: %d %s", code, body)
	}

	code, body = e.do("POST", "/api/digest/send", testToken, nil)
	if code != 200 || len(e.sent) != 1 {
		t.Fatalf("send: %d %s", code, body)
	}
	msg := string(e.sent[0])
	for _, want := range []string{"Subject: [movieSelector] 2 new titles requested", "multipart/alternative", "The Matrix (1999)", "text/html"} {
		if !strings.Contains(msg, want) {
			t.Errorf("email missing %q", want)
		}
	}

	// Already-notified titles aren't repeated; new ones are.
	if n, _ := e.notify.SendDigest(ctx); n != 0 {
		t.Fatalf("re-sent %d", n)
	}
	e.request("sam", dune)
	if n, _ := e.notify.SendDigest(ctx); n != 1 {
		t.Fatalf("want 1 new, got %d", n)
	}
}

func TestDailySchedule(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	e.request("sam", matrix)
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local)

	e.notify.maybeRunDaily(ctx, day.Add(8*time.Hour+59*time.Minute), 9, 0)
	if len(e.sent) != 0 {
		t.Fatal("sent before scheduled time")
	}
	e.notify.maybeRunDaily(ctx, day.Add(9*time.Hour), 9, 0)
	if len(e.sent) != 1 {
		t.Fatal("not sent at scheduled time")
	}
	e.request("sam", dune)
	e.notify.maybeRunDaily(ctx, day.Add(15*time.Hour), 9, 0)
	if len(e.sent) != 1 {
		t.Fatal("sent twice in one day")
	}
	e.notify.maybeRunDaily(ctx, day.Add(33*time.Hour), 9, 0) // next day 09:00
	if len(e.sent) != 2 {
		t.Fatal("not sent the next day")
	}
}

func TestFolderName(t *testing.T) {
	cases := []struct {
		title string
		year  int
		id    int
		want  string
	}{
		{"The Matrix", 1999, 603, "The Matrix (1999) {tmdb-603}"},
		{"Mad Max: Fury Road", 2015, 76341, "Mad Max - Fury Road (2015) {tmdb-76341}"},
		{"What If...?", 2021, 91363, "What If... (2021) {tmdb-91363}"},
		{"AC/DC: Live", 0, 0, "AC-DC - Live"},
	}
	for _, c := range cases {
		if got := FolderName(c.title, c.year, c.id); got != c.want {
			t.Errorf("FolderName(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestStaticPages(t *testing.T) {
	e := newTestEnv(t)
	for _, p := range []string{"/", "/manage", "/static/app.js", "/demo-poster/movie/603.svg"} {
		if code, _ := e.do("GET", p, "", nil); code != 200 {
			t.Errorf("GET %s: %d", p, code)
		}
	}
}

func TestDigestSendGrid(t *testing.T) {
	e := newTestEnv(t)
	var got struct {
		auth string
		body map[string]any
	}
	status := http.StatusAccepted
	sg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(status)
		if status != http.StatusAccepted {
			w.Write([]byte(`{"errors":[{"message":"The from address does not match a verified Sender Identity."}]}`))
		}
	}))
	defer sg.Close()
	e.notify.SMTP = SMTPConfig{From: "movieSelector <ms@example.com>"}
	e.notify.SendGridKey = "SG.test"
	e.notify.sendGridURL = sg.URL

	e.request("sam", matrix)
	status = http.StatusForbidden
	if _, err := e.notify.SendDigest(context.Background()); err == nil || !strings.Contains(err.Error(), "verified Sender") {
		t.Fatalf("want sendgrid error, got %v", err)
	}

	status = http.StatusAccepted
	if n, err := e.notify.SendDigest(context.Background()); err != nil || n != 1 {
		t.Fatalf("send: n=%d err=%v", n, err)
	}
	if got.auth != "Bearer SG.test" {
		t.Errorf("auth header %q", got.auth)
	}
	b, _ := json.Marshal(got.body)
	for _, want := range []string{`"subject":"[movieSelector] 1 new title requested"`, `"email":"me@example.com"`,
		`"from":{"email":"ms@example.com","name":"movieSelector"}`, `"type":"text/html"`, "The Matrix (1999)"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("payload missing %s: %s", want, b)
		}
	}
	if len(e.sent) != 0 {
		t.Errorf("SMTP used alongside SendGrid")
	}
}
