FROM golang:1.27 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Static binary; tzdata is embedded since the slim image may lack zoneinfo.
RUN CGO_ENABLED=0 go build -tags timetzdata -trimpath -ldflags="-s -w" -o /dashboard ./cmd/dashboard

FROM oven/bun:1.4-slim AS deps
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --production

FROM oven/bun:1.4-slim
WORKDIR /app
COPY --from=deps /app/node_modules node_modules
COPY package.json ./
COPY renderer renderer
COPY pages pages
COPY --from=go /dashboard /usr/local/bin/dashboard
RUN mkdir /data && chown bun:bun /data
USER bun
VOLUME /data
EXPOSE 8080 8081
# Assumes the admin listener on port 8081.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
  CMD ["bun", "-e", "const r = await fetch('http://127.0.0.1:8081/healthz'); process.exit(r.ok ? 0 : 1)"]
ENTRYPOINT ["dashboard"]
