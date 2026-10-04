// movieSelector is a tiny self-hosted web app for requesting movies and shows.
// Requests land in a SQLite queue that a maintainer (or an AI agent, via the
// JSON API) works through, and a daily email digest announces new requests.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // so TZ works inside a scratch container
)

type Config struct {
	Addr       string
	DBPath     string
	AdminToken string
	TMDBKey    string
	BaseURL    string // public URL used for links in emails

	SMTP        SMTPConfig
	SendGridKey string // when set, used instead of SMTP
	NotifyTo    []string
	NotifyAt    string // "HH:MM" local time; empty disables the daily digest
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func loadConfig() Config {
	port, _ := strconv.Atoi(env("SMTP_PORT", "587"))
	c := Config{
		Addr:       env("LISTEN_ADDR", ":8080"),
		DBPath:     env("DB_PATH", "data/movies.db"),
		AdminToken: env("ADMIN_TOKEN", ""),
		TMDBKey:    env("TMDB_API_KEY", ""),
		BaseURL:    strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		SMTP: SMTPConfig{
			Host:     env("SMTP_HOST", ""),
			Port:     port,
			Username: env("SMTP_USERNAME", ""),
			Password: env("SMTP_PASSWORD", ""),
			From:     env("SMTP_FROM", "movieselector@localhost"),
			TLS:      env("SMTP_TLS", ""),
		},
		SendGridKey: env("SENDGRID_API_KEY", ""),
		NotifyAt:    env("NOTIFY_AT", "09:00"),
	}
	for _, addr := range strings.Split(env("NOTIFY_EMAIL", ""), ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			c.NotifyTo = append(c.NotifyTo, addr)
		}
	}
	return c
}

func main() {
	cfg := loadConfig()

	if cfg.AdminToken == "" {
		b := make([]byte, 16)
		rand.Read(b)
		cfg.AdminToken = hex.EncodeToString(b)
		log.Printf("ADMIN_TOKEN not set; generated one for this run: %s", cfg.AdminToken)
	}

	if dir := filepath.Dir(cfg.DBPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("create data dir: %v", err)
		}
	}
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var provider Provider
	if cfg.TMDBKey != "" {
		provider = NewTMDB(cfg.TMDBKey)
		log.Printf("search provider: TMDB")
	} else {
		provider = NewDemoProvider()
		log.Printf("search provider: built-in demo catalog (set TMDB_API_KEY for real search)")
	}

	notifier := &Notifier{Store: store, SMTP: cfg.SMTP, SendGridKey: cfg.SendGridKey, To: cfg.NotifyTo, BaseURL: cfg.BaseURL}
	if !notifier.Enabled() {
		log.Printf("email not configured (SENDGRID_API_KEY or SMTP_HOST, plus NOTIFY_EMAIL); digests will be logged instead")
	} else if cfg.SendGridKey != "" {
		log.Printf("email: sending digests through SendGrid")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.NotifyAt != "" && cfg.NotifyAt != "off" {
		go notifier.RunDaily(ctx, cfg.NotifyAt)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           NewServer(store, provider, notifier, cfg.AdminToken).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
