# Build Stage - Compile the static Go binary.
FROM casjaysdev/go:latest AS builder

ARG TARGETARCH
ARG VERSION=dev
ARG BUILD_DATE
ARG BUILD_EPOCH
ARG COMMIT_ID

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -buildvcs=false -trimpath \
    -ldflags "-s -w -X 'main.Version=${VERSION}' -X 'main.CommitID=${COMMIT_ID}' -X 'main.BuildEpoch=${BUILD_EPOCH}'" \
    -o /app/binary/countries ./src

# Runtime Stage - Minimal Alpine image.
FROM alpine:latest

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    tini

COPY --from=builder /app/binary/countries /usr/local/bin/countries
COPY docker/rootfs/ /
COPY docker/Dockerfile /root/Dockerfile

RUN chmod 755 /usr/local/bin/*

ENV TZ=America/New_York \
    PORT=80 \
    CONFIG_DIR=/config \
    DATA_DIR=/data \
    LOGS_DIR=/logs \
    DATABASE_DIR=/data/db/sqlite

EXPOSE 80
STOPSIGNAL SIGRTMIN+3

HEALTHCHECK --start-period=30s --interval=30s --timeout=5s --retries=3 \
    CMD /usr/local/bin/countries --port 80 --status || exit 1

ENTRYPOINT [ "tini", "-p", "SIGTERM", "--", "/usr/local/bin/entrypoint.sh" ]
