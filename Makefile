# Run inside `nix develop`.
-include .env
export

.PHONY: dev mail test build docker clean

dev: ## run the server with live data dir ./data
	go run .

mail: ## local SMTP catcher: SMTP on :1025, web inbox on http://localhost:8025
	mailpit --smtp 127.0.0.1:1025 --listen 127.0.0.1:8025

test:
	go vet ./...
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/movieselector .

docker:
	docker build -t movieselector .

clean:
	rm -rf bin
