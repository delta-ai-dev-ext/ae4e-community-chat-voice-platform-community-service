# syntax=docker/dockerfile:1

# Build stage: compile a static binary with the toolchain pinned by go.mod.
FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/community_service ./cmd/server

# Runtime stage: distroless, non-root, no shell and no package manager.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/community_service /community_service

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/community_service"]
