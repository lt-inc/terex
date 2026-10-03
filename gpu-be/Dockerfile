# Build context: terex/ (needs ../proto). Example: docker build -f gpu-be/Dockerfile .
#
# Тяжёлые зависимости (CUDA/torch/whisper/yt-dlp/YAMNet) НЕ в образе — ставятся
# один раз руками прямо на хосте GPU-ноды (ru-lt-inc) и монтируются в контейнер
# через hostPath (см. k3s/admin/terex-gpu-be.yaml). Причина: torch+CUDA вес
# несколько ГБ, пуш/пул через небогатый по трафику VPS-registry был регулярной
# болью (413/499/disk-pressure/таймауты). Здесь образ лёгкий — тот же Ubuntu,
# что и на хосте (ABI/glibc должны совпадать с хостовыми .so в site-packages),
# плюс ffmpeg и системный python3 (они хоть и из hostPath-монтируемого
# ~/.local не ставятся, но интерпретатор должен быть той же версии, что
# собирала С-расширения в site-packages).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY proto ./proto
COPY gpu-be ./gpu-be
# go.work lists main-be too; stub it so the workspace resolves
RUN mkdir -p main-be && printf 'module terex/main-be\n\ngo 1.26\n' > main-be/go.mod
RUN cd gpu-be && CGO_ENABLED=0 go build -o /out/gpu-be ./cmd/gpu-be

FROM ubuntu:26.04
RUN apt-get update && apt-get install -y --no-install-recommends \
      ffmpeg python3 ca-certificates fonts-liberation \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/gpu-be /usr/local/bin/gpu-be
ENTRYPOINT ["gpu-be"]
