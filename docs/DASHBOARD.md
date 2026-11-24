# Configurable dashboard (AMIS)

Agentiplex embeds the published [AMIS](https://github.com/baidu/amis) 6.13.0 SDK (Apache-2.0) and renders [dashboard.json](../internal/dashboard/static/dashboard.json). No handwritten CSS, custom visual components, React application or separate frontend runtime.

## Build

Run `go generate ./internal/dashboard` before `go build ./cmd/agentiplexd` or `go test ./...`. The Taskfile, demo, Checks and Docker build package the assets automatically. Node/npm and network access are build-time only. The script fetches the exact npm release with npm's integrity verification and disables lifecycle scripts, copies the unmodified SDK and package license, and ignores generated files in Git. The binary embeds the assets; no CDN, Node or frontend service is needed at runtime. A bare Go build without generated assets is possible for headless usage; start with --dashboard=false or package assets first. An enabled dashboard without assets fails startup with an actionable error.

## Configuration

```yaml
observability:
  dashboard: true
  dashboard_schema: ""           # path to a trusted AMIS JSON page
  dashboard_theme: cxd           # cxd, antd, ang, dark
  dashboard_refresh_interval: 2s # default schema, 1s–1m
```

Equivalent daemon flags: --dashboard, --dashboard-schema, --dashboard-theme, --dashboard-refresh-interval. Configuration is loaded at startup. Custom schemas control their own component refresh intervals. Export the built-in schema from the repository, edit declarative AMIS components and restart the daemon to apply it. No Go or CSS changes required. [AMIS component documentation](https://aisuda.bce.baidu.com/amis/docs).

## Data and limitations

GET /api/v1/dashboard returns a bounded metadata-only snapshot with agent/collection/delivery/event tables and counts. GET /api/v1/dashboard/detail/{id} returns the selected record as plain text JSON. The original REST and SSE interfaces remain unchanged; the UI uses AMIS service polling rather than a custom SSE adapter. It is not a production monitoring index. The UI shows recent retained events and no claimed online status.

The SDK compiles schema expressions and uses inline styles; its CSP therefore allows unsafe-eval and unsafe-inline styles, while restricting network connections and scripts to the local origin. Treat operator schemas as executable trusted configuration, never accept them from agents or untrusted sources. The bootstrap fetcher allows same-origin GET requests to /api/v1/ only; it is not a security sandbox for arbitrary schemas. Agentiplex remains unauthenticated trusted-local software. Full bundled third-party dependency/license and security review is required before public deployment. The SDK package is substantially larger than the handwritten UI; this is an explicit tradeoff for reusing a proven design system.
