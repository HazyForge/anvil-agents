# syntax=docker/dockerfile:1.7
# Build from the anvil-agents repository root:
#   docker build -f web/site/Dockerfile -t ghcr.io/hazyforge/anvil-agents-site:dev .

FROM node:22-alpine AS build
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@10.25.0 --activate

COPY web/site/package.json web/site/pnpm-lock.yaml web/site/
WORKDIR /src/web/site
RUN pnpm install --frozen-lockfile

WORKDIR /src
COPY docs/ docs/
COPY web/site/ web/site/
WORKDIR /src/web/site
RUN pnpm build

FROM nginxinc/nginx-unprivileged:1.27-alpine AS runtime
COPY web/site/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/web/site/dist /usr/share/nginx/html
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
