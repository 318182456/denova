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
    && go build -trimpath -o output/denova-container-init ./docker/init-config.go

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git python3 chromium fonts-noto-cjk tini tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 denova && useradd --uid 1000 --gid 1000 --home-dir /data --shell /bin/bash denova \
    && mkdir -p /data/.denova && chown -R denova:denova /data
# Claude Code CLI for the Claude runtime. The publish workflow pins the exact
# version so layer caching never serves a stale CLI. Credentials created by
# `claude auth login` live in the persisted /data/.claude.
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
RUN chmod 755 /usr/local/bin/denova-entrypoint /usr/local/bin/chromium
ENV DENOVA_DIR=/data/.denova HOME=/data DISABLE_AUTOUPDATER=1 PATH=/opt/denova:/opt/denova/tools:/usr/local/bin:/usr/bin:/bin
WORKDIR /data
USER denova
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=10 CMD curl -fsS http://127.0.0.1:8080/api/auth/status >/dev/null || exit 1
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/denova-entrypoint"]
CMD ["/opt/denova/denova", "--no-open", "--port", "8080"]
