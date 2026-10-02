# Build context: terex/ (needs ../proto). Example: docker build -f gpu-be/Dockerfile .
# Local (no GPU): --build-arg RUNTIME_BASE=ubuntu:22.04
ARG RUNTIME_BASE=nvidia/cuda:12.4.1-runtime-ubuntu22.04

FROM golang:1.26 AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY proto ./proto
COPY gpu-be ./gpu-be
# go.work lists main-be too; stub it so the workspace resolves
RUN mkdir -p main-be && printf 'module terex/main-be\n\ngo 1.26\n' > main-be/go.mod
RUN cd gpu-be && CGO_ENABLED=0 go build -o /out/gpu-be ./cmd/gpu-be

# CUDA runtime so ffmpeg NVENC / whisper can use the GPU on the GPU host.
FROM ${RUNTIME_BASE}
RUN apt-get update && apt-get install -y --no-install-recommends \
      ffmpeg python3 python3-pip ca-certificates curl fonts-liberation \
    && pip3 install --no-cache-dir yt-dlp openai-whisper ai-edge-litert numpy \
    && rm -rf /var/lib/apt/lists/*

# YAMNet (TFLite, ~4 MB) for audio-event hints (laugh/shout/gasp/music). If the download fails the build still succeeds:
# gpu-be logs a warning and the LLM just gets fewer hints.
RUN mkdir -p /opt/terex \
    && (curl -fsSL -o /opt/terex/yamnet.tflite https://storage.googleapis.com/download.tensorflow.org/models/tflite/task_library/audio_classification/android/lite-model_yamnet_classification_tflite_1.tflite \
        && curl -fsSL -o /opt/terex/yamnet_class_map.csv https://raw.githubusercontent.com/tensorflow/models/master/research/audioset/yamnet/yamnet_class_map.csv \
        || echo "WARNING: YAMNet download failed; audio-event hints disabled")
COPY gpu-be/tools/yamnet.py /opt/terex/yamnet.py
COPY --from=build /out/gpu-be /usr/local/bin/gpu-be
ENTRYPOINT ["gpu-be"]
