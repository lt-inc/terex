# Build context: terex/ (needs ../proto). Example: docker build -f main-be/Dockerfile .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY proto ./proto
COPY main-be ./main-be
# go.work lists gpu-be too; stub it so the workspace resolves
RUN mkdir -p gpu-be && printf 'module terex/gpu-be\n\ngo 1.26\n' > gpu-be/go.mod
RUN cd main-be && CGO_ENABLED=0 go build -o /out/main-be ./cmd/main-be

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/main-be /main-be
EXPOSE 8000
ENTRYPOINT ["/main-be"]
