FROM registry.access.redhat.com/hi/go@sha256:053da9c6e7e5234362b167163057f328b04aaf69a3baaf775315101e2fa5f52a AS build

ARG BUILD_DATE=unknown
ARG BUILD_REVISION=unknown
ARG BUILD_VERSION=dev

WORKDIR /workspace
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -tags "netgo osusergo" -trimpath \
    -ldflags "-s -w -X github.com/elsell/reqdb/internal/buildinfo.Version=${BUILD_VERSION} -X github.com/elsell/reqdb/internal/buildinfo.Revision=${BUILD_REVISION} -X github.com/elsell/reqdb/internal/buildinfo.BuildDate=${BUILD_DATE}" \
    -o /workspace/reqdb ./cmd/reqdb
RUN mkdir -p /workspace/data /workspace/tmp /workspace/runtime/lib64 && \
    cp -L /lib64/ld-linux-x86-64.so.2 /workspace/runtime/lib64/ && \
    cp -L /lib64/libc.so.6 /workspace/runtime/lib64/

FROM scratch

ARG BUILD_DATE=unknown
ARG BUILD_REVISION=unknown
ARG BUILD_VERSION=dev

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.description="Requirements database server and CLI" \
      org.opencontainers.image.revision="${BUILD_REVISION}" \
      org.opencontainers.image.source="https://github.com/elsell/reqdb" \
      org.opencontainers.image.title="reqdb" \
      org.opencontainers.image.version="${BUILD_VERSION}"

COPY --from=build /workspace/reqdb /usr/local/bin/reqdb
COPY --from=build /workspace/runtime/ /
COPY --chown=65532:65532 --from=build /workspace/data /data
COPY --chown=65532:65532 --from=build /workspace/tmp /tmp
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/reqdb"]
CMD ["serve", "--db", "/data/reqdb.sqlite", "--listen", "0.0.0.0:8080"]
