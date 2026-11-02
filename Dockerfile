# docker build -t opencloud-mcp:dev .  (or: mise run image)
ARG GO_VERSION=1.27.3

FROM golang:${GO_VERSION}-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /opencloud-mcp ./cmd/opencloud-mcp

FROM alpine:3.24

RUN apk add --no-cache ca-certificates

LABEL org.opencontainers.image.title="opencloud-mcp" \
      org.opencontainers.image.description="MCP server for OpenCloud spaces" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /opencloud-mcp /usr/bin/opencloud-mcp

USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/usr/bin/opencloud-mcp"]
