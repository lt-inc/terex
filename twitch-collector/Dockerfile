FROM golang:1.26-bookworm AS build
WORKDIR /src
ENV GOWORK=off
COPY proto ./proto
COPY twitch-collector ./twitch-collector
WORKDIR /src/twitch-collector
RUN go mod download && CGO_ENABLED=0 go build -trimpath -o /collector ./cmd/collector

FROM python:3.12-slim-bookworm
ARG YTDLP_VERSION=2026.08.19
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir "yt-dlp==${YTDLP_VERSION}" \
    && groupadd --gid 10001 collector && useradd --uid 10001 --gid collector --no-create-home collector \
    && mkdir -p /tmp/twitch-collector && chown collector:collector /tmp/twitch-collector
COPY --from=build /collector /usr/local/bin/collector
USER 10001:10001
ENV HTTP_ADDR=:8080 TMP_DIR=/tmp/twitch-collector
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/collector"]
