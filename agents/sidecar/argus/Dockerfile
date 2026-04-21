# Argus sidecar image. Tiny stateless heartbeat emitter compiled static.

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY agents/sidecar/argus ./agents/sidecar/argus
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /out/argus-sidecar ./agents/sidecar/argus

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/argus-sidecar /argus-sidecar
USER 65532:65532
ENTRYPOINT ["/argus-sidecar"]
