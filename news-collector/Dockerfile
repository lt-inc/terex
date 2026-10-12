FROM golang:1.26-bookworm AS build
WORKDIR /src
ENV GOWORK=off
COPY proto ./proto
COPY news-collector ./news-collector
WORKDIR /src/news-collector
RUN go mod download && CGO_ENABLED=0 go build -trimpath -o /collector ./cmd/collector

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /collector /usr/local/bin/news-collector
USER 65532:65532
ENV HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/news-collector"]
