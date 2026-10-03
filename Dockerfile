# Build: docker build -t movieselector .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /movieselector . \
 && mkdir -p /data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /movieselector /movieselector
COPY --from=build --chown=568:568 /data /data
# 568 is the "apps" user on TrueNAS SCALE; override with --user if needed.
USER 568:568
ENV LISTEN_ADDR=:8080 DB_PATH=/data/movies.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/movieselector"]
