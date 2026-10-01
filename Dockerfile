# syntax=docker/dockerfile:1
# Toolchain versions default to the current sources; the publish workflow reads
# them from go.mod and web/package.json so upstream upgrades need no edit here.
ARG GO_VERSION=1.26.6
ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-bookworm AS node
FROM golang:${GO_VERSION}-bookworm AS build
ARG PNPM_VERSION=11.15.1
ARG DENOVA_VERSION=
COPY --from=node /usr/local/ /usr/local/
RUN npm install -g pnpm@${PNPM_VERSION}
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
COPY agent/go.mod agent/go.sum ./agent/
RUN go mod download && cd agent && go mod download
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
COPY web/patches ./web/patches
RUN pnpm --dir web install --frozen-lockfile
COPY . .
RUN DENOVA_VERSION="${DENOVA_VERSION}" bash scripts/build.sh \
    && go build -trimpath -o output/denova-container-init ./docker/init-config.go \
    && go build -trimpath -o output/denova-claude-page ./docker/claude-page

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git python3 chromium fonts-noto-cjk tini tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 denova && useradd --uid 1000 --gid 1000 --home-dir /data --shell /bin/bash denova \
    && mkdir -p /data/.denova && chown -R denova:denova /data
# Claude Code CLI for the Claude runtime. The publish workflow pins the exact
# version so layer caching never serves a stale CLI. Credentials created by
# the Claude page live in the persisted /data/.claude; its updates install into
# /data/.local/bin, which precedes this copy on PATH. /opt/denova/launcher/claude
# fronts both to apply the token selected on that page.
ARG CLAUDE_CODE_VERSION=latest
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/claude-install.sh \
    && HOME=/tmp/claude-home bash /tmp/claude-install.sh "${CLAUDE_CODE_VERSION}" \
    && install -m 755 "$(readlink -f /tmp/claude-home/.local/bin/claude)" /usr/local/bin/claude \
    && rm -rf /tmp/claude-install.sh /tmp/claude-home \
    && claude --version
COPY --from=build /src/output/ /opt/denova/
COPY LICENSE /opt/denova/LICENSE
COPY docker/entrypoint.sh /usr/local/bin/denova-entrypoint
COPY docker/chromium.sh /usr/local/bin/chromium
COPY docker/claude-launcher.sh /opt/denova/launcher/claude
RUN chmod 755 /usr/local/bin/denova-entrypoint /usr/local/bin/chromium /opt/denova/launcher/claude
ENV DENOVA_DIR=/data/.denova HOME=/data DISABLE_AUTOUPDATER=1 DENOVA_PROXY_PORT=8080 DENOVA_BACKEND_PORT=18080 PATH=/opt/denova/launcher:/data/.local/bin:/opt/denova:/opt/denova/tools:/usr/local/bin:/usr/bin:/bin
WORKDIR /data
USER denova
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=10 CMD curl -fsS http://127.0.0.1:8080/api/auth/status >/dev/null || exit 1
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/denova-entrypoint"]
CMD ["/opt/denova/denova", "--no-open", "--port", "18080"]
