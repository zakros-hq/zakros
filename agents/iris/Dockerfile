# Iris conversational pod. Long-running Go binary that long-polls
# Hermes, calls Claude, and forwards commissions to Minos.
#
# Phase 1 / Slice 0: Claude-backed inference (no Athena Ollama yet).
# Phase 3 swaps backend at the env-var level — image stays the same.

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY agents/iris ./agents/iris
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /out/iris ./agents/iris/cmd/iris

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/iris /iris
USER 65532:65532
ENTRYPOINT ["/iris"]
