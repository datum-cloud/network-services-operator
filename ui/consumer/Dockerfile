FROM oven/bun:1 AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . .
RUN bun run build

FROM oven/bun:1-slim
RUN apt-get update && apt-get install -y --no-install-recommends --only-upgrade ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY serve.ts ./
USER bun
EXPOSE 8443
CMD ["bun", "run", "serve.ts"]
