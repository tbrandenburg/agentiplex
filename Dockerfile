FROM node:22-bookworm-slim AS ui
WORKDIR /src
COPY scripts/dashboard-assets.sh scripts/
COPY third_party/amis/LICENSE third_party/amis/LICENSE
RUN bash scripts/dashboard-assets.sh

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/dashboard/static/vendor/ internal/dashboard/static/vendor/
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/agentiplexd ./cmd/agentiplexd && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/agentiplex ./cmd/agentiplex

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && \
    mkdir /data && chown 65532:65532 /data
COPY --from=build /out/agentiplexd /usr/local/bin/agentiplexd
COPY --from=build /out/agentiplex /usr/local/bin/agentiplex
USER 65532:65532
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["agentiplexd"]
CMD ["--listen", "0.0.0.0:8080", "--trusted-network", "--public-url", "http://127.0.0.1:8080", "--storage", "/data"]
