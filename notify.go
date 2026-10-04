package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS is "tls" (implicit TLS, usually port 465), "starttls" (require
	// STARTTLS), "none" (plain, e.g. a local relay) or "" (auto: implicit TLS
	// on 465, otherwise STARTTLS when the server offers it).
	TLS string
}

// Notifier sends the daily digest of newly requested titles.
type Notifier struct {
	Store *Store
	SMTP  SMTPConfig
	// SendGridKey, when set, sends through the SendGrid Web API instead of
	// SMTP. SMTP.From is still the sender and must be verified in SendGrid.
	SendGridKey string
	To          []string
	BaseURL     string

	mu sync.Mutex // one digest at a time
	// send and sendGridURL are swappable for tests.
	send        func(cfg SMTPConfig, to []string, msg []byte) error
	sendGridURL string
}

func (n *Notifier) Enabled() bool {
	return (n.SendGridKey != "" || n.SMTP.Host != "") && len(n.To) > 0
}

type Digest struct {
	Subject  string
	Text     string
	HTML     string
	Requests []*Request
}

// BuildDigest renders the digest for all requests not yet notified.
// Returns nil if there is nothing new.
func (n *Notifier) BuildDigest(ctx context.Context) (*Digest, error) {
	reqs, err := n.Store.Unnotified(ctx)
	if err != nil || len(reqs) == 0 {
		return nil, err
	}
	d := &Digest{Requests: reqs}
	noun := "titles"
	if len(reqs) == 1 {
		noun = "title"
	}
	d.Subject = fmt.Sprintf("[movieSelector] %d new %s requested", len(reqs), noun)

	var t strings.Builder
	fmt.Fprintf(&t, "%d new %s requested since the last digest:\n\n", len(reqs), noun)
	for _, r := range reqs {
		fmt.Fprintf(&t, "- %s [%s]\n", r.DisplayName, r.MediaType)
		fmt.Fprintf(&t, "    folder: %s\n", r.FolderName)
		if r.IMDBID != "" {
			fmt.Fprintf(&t, "    imdb:   https://www.imdb.com/title/%s/\n", r.IMDBID)
		}
		fmt.Fprintf(&t, "    by:     %s (requested %dx), status: %s\n", orDash(r.RequestedBy), r.RequestCount, r.Status)
	}
	fmt.Fprintf(&t, "\nManage the queue: %s/manage\nAPI: %s/api/queue\n", n.BaseURL, n.BaseURL)
	d.Text = t.String()

	var h bytes.Buffer
	err = digestHTML.Execute(&h, map[string]any{"Requests": reqs, "BaseURL": n.BaseURL, "Count": len(reqs), "Noun": noun})
	d.HTML = h.String()
	return d, err
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var digestHTML = template.Must(template.New("digest").Funcs(template.FuncMap{
	"abs": func(base, u string) string {
		if strings.HasPrefix(u, "/") {
			return base + u
		}
		return u
	},
}).Parse(`<!doctype html><html><body style="margin:0;background:#f4f4f7;font-family:-apple-system,Segoe UI,Roboto,sans-serif;color:#1d1d22">
<div style="max-width:640px;margin:0 auto;padding:24px">
<h1 style="font-size:20px;margin:0 0 4px">{{.Count}} new {{.Noun}} requested</h1>
<p style="margin:0 0 20px;color:#666">Since the last digest.</p>
{{range .Requests}}<table cellpadding="0" cellspacing="0" style="width:100%;background:#fff;border-radius:10px;margin-bottom:12px"><tr>
<td style="width:92px;padding:12px">{{if .PosterURL}}<img src="{{abs $.BaseURL .PosterURL}}" width="80" style="border-radius:6px;display:block" alt="">{{end}}</td>
<td style="padding:12px 12px 12px 0;vertical-align:top">
<div style="font-weight:600;font-size:16px">{{.DisplayName}} <span style="font-size:11px;background:#eee;border-radius:4px;padding:2px 6px;color:#555;text-transform:uppercase">{{.MediaType}}</span></div>
<div style="font-family:monospace;font-size:12px;color:#555;margin:4px 0">{{.FolderName}}</div>
<div style="font-size:13px;color:#555">Requested by {{if .RequestedBy}}{{.RequestedBy}}{{else}}someone{{end}}{{if gt .RequestCount 1}} ({{.RequestCount}}×){{end}}{{if .IMDBID}} · <a href="https://www.imdb.com/title/{{.IMDBID}}/">IMDb</a>{{end}}</div>
</td></tr></table>
{{end}}
<p style="margin-top:20px"><a href="{{.BaseURL}}/manage" style="background:#e50914;color:#fff;text-decoration:none;padding:10px 16px;border-radius:8px;display:inline-block">Open the queue</a></p>
</div></body></html>`))

// SendDigest builds and sends the digest, then marks the included requests as
// notified. If email isn't configured, the digest is logged instead (and still
// marked) so the daily cycle can be tested without SMTP. Returns the number of
// requests included.
func (n *Notifier) SendDigest(ctx context.Context) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	d, err := n.BuildDigest(ctx)
	if err != nil || d == nil {
		return 0, err
	}
	if n.Enabled() {
		if n.SendGridKey != "" {
			err = n.sendGrid(ctx, d)
		} else {
			send := n.send
			if send == nil {
				send = sendMail
			}
			err = send(n.SMTP, n.To, buildMessage(n.SMTP.From, n.To, d))
		}
		if err != nil {
			return 0, fmt.Errorf("send digest: %w", err)
		}
		log.Printf("digest: emailed %d request(s) to %s", len(d.Requests), strings.Join(n.To, ", "))
	} else {
		log.Printf("digest (email not configured):\n%s", d.Text)
	}
	ids := make([]int64, len(d.Requests))
	for i, r := range d.Requests {
		ids[i] = r.ID
	}
	return len(ids), n.Store.MarkNotified(ctx, ids)
}

const metaLastDigest = "last_digest_date"

// RunDaily sends the digest once per day at the given local time ("HH:MM").
// The last run date is stored in the database, so restarts never cause a
// double send, and a digest missed while the server was down is sent on startup.
func (n *Notifier) RunDaily(ctx context.Context, at string) {
	hh, mm, err := parseClock(at)
	if err != nil {
		log.Printf("digest scheduler disabled: NOTIFY_AT %q: %v", at, err)
		return
	}
	log.Printf("digest scheduled daily at %02d:%02d %s", hh, mm, time.Now().Location())
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		n.maybeRunDaily(ctx, time.Now(), hh, mm)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (n *Notifier) maybeRunDaily(ctx context.Context, t time.Time, hh, mm int) {
	due := time.Date(t.Year(), t.Month(), t.Day(), hh, mm, 0, 0, t.Location())
	if t.Before(due) {
		return
	}
	today := t.Format("2006-01-02")
	last, err := n.Store.GetMeta(ctx, metaLastDigest)
	if err != nil || last == today {
		return
	}
	if _, err := n.SendDigest(ctx); err != nil {
		log.Printf("daily digest failed (will retry in a minute): %v", err)
		return
	}
	if err := n.Store.SetMeta(ctx, metaLastDigest, today); err != nil {
		log.Printf("record digest date: %v", err)
	}
}

func parseClock(s string) (int, int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("want HH:MM")
	}
	return hh, mm, nil
}

func buildMessage(from string, to []string, d *Digest) []byte {
	b := make([]byte, 12)
	rand.Read(b)
	boundary := "ms-" + hex.EncodeToString(b)
	var m bytes.Buffer
	fmt.Fprintf(&m, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		from, strings.Join(to, ", "), d.Subject, time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&m, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	for _, part := range []struct{ typ, body string }{{"text/plain", d.Text}, {"text/html", d.HTML}} {
		fmt.Fprintf(&m, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.typ)
		qp := quotedprintable.NewWriter(&m)
		qp.Write([]byte(part.body))
		qp.Close()
		m.WriteString("\r\n")
	}
	fmt.Fprintf(&m, "--%s--\r\n", boundary)
	return m.Bytes()
}

func sendMail(cfg SMTPConfig, to []string, msg []byte) error {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host}
	mode := cfg.TLS
	if mode == "" && cfg.Port == 465 {
		mode = "tls"
	}

	var c *smtp.Client
	if mode == "tls" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		if c, err = smtp.NewClient(conn, cfg.Host); err != nil {
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			return err
		}
		if c, err = smtp.NewClient(conn, cfg.Host); err != nil {
			return err
		}
		if ok, _ := c.Extension("STARTTLS"); ok && mode != "none" {
			if err := c.StartTLS(tlsCfg); err != nil {
				return err
			}
		} else if mode == "starttls" {
			return fmt.Errorf("server does not support STARTTLS")
		}
	}
	defer c.Close()

	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return err
		}
	}
	from := cfg.From
	if a, err := mail.ParseAddress(cfg.From); err == nil {
		from = a.Address
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

const sendGridEndpoint = "https://api.sendgrid.com/v3/mail/send"

type sendGridAddr struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// sendGrid sends the digest through the SendGrid v3 mail/send API.
func (n *Notifier) sendGrid(ctx context.Context, d *Digest) error {
	from := sendGridAddr{Email: n.SMTP.From}
	if a, err := mail.ParseAddress(n.SMTP.From); err == nil {
		from = sendGridAddr{Email: a.Address, Name: a.Name}
	}
	to := make([]sendGridAddr, len(n.To))
	for i, addr := range n.To {
		to[i] = sendGridAddr{Email: addr}
	}
	body, err := json.Marshal(map[string]any{
		"personalizations": []any{map[string]any{"to": to}},
		"from":             from,
		"subject":          d.Subject,
		"content": []map[string]string{
			{"type": "text/plain", "value": d.Text},
			{"type": "text/html", "value": d.HTML},
		},
	})
	if err != nil {
		return err
	}
	url := n.sendGridURL
	if url == "" {
		url = sendGridEndpoint
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.SendGridKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("sendgrid: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}
