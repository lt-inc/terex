# Build context: terex/ (needs ../proto). Example: docker build -f gpu-be/Dockerfile .
#
# Тяжёлые зависимости (CUDA/ffmpeg/python/torch/whisper/YAMNet) вынесены в
# отдельный base-образ (Dockerfile.base), который собирается вручную и редко —
# иначе CI на каждый пуш гонял полную установку torch+whisper, что регулярно
# валило GitHub-раннер по месту на диске/памяти при push (даже после очистки
# диска). Здесь — только компиляция Go-бинарника и докладка его в base-образ.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY proto ./proto
COPY gpu-be ./gpu-be
# go.work lists main-be too; stub it so the workspace resolves
RUN mkdir -p main-be && printf 'module terex/main-be\n\ngo 1.26\n' > main-be/go.mod
RUN cd gpu-be && CGO_ENABLED=0 go build -o /out/gpu-be ./cmd/gpu-be

FROM registry.legendary-trio.me/lt-inc/terex-gpu-be-base:latest
COPY --from=build /out/gpu-be /usr/local/bin/gpu-be
ENTRYPOINT ["gpu-be"]
