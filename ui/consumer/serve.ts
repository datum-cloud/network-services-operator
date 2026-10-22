import { createReadStream, readFileSync } from "node:fs";
import { stat } from "node:fs/promises";
import type { IncomingMessage, ServerResponse } from "node:http";
import { createServer } from "node:https";
import { extname, join, resolve, sep } from "node:path";

const DIST_DIR = resolve(import.meta.dirname, "dist");
const TLS_DIR = "/etc/tls";
const PORT = 8443;
const HOST = "0.0.0.0";

const MIME_TYPES: Record<string, string> = {
  ".html": "text/html;charset=utf-8",
  ".js": "text/javascript;charset=utf-8",
  ".mjs": "text/javascript;charset=utf-8",
  ".cjs": "text/javascript;charset=utf-8",
  ".ts": "text/javascript;charset=utf-8",
  ".css": "text/css;charset=utf-8",
  ".json": "application/json;charset=utf-8",
  ".map": "application/json;charset=utf-8",
  ".txt": "text/plain;charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".ico": "image/x-icon",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
  ".wasm": "application/wasm",
  ".zip": "application/zip",
};

function notFound(res: ServerResponse): void {
  res.writeHead(404, { "content-type": "text/plain;charset=utf-8" });
  res.end("Not Found");
}

async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
  const { pathname } = new URL(req.url ?? "/", "https://localhost");
  const filePath = resolve(join(DIST_DIR, pathname));
  if (!filePath.startsWith(DIST_DIR + sep)) {
    notFound(res);
    return;
  }

  const info = await stat(filePath).catch(() => null);
  if (!info?.isFile()) {
    notFound(res);
    return;
  }

  res.writeHead(200, {
    "content-type":
      MIME_TYPES[extname(filePath).toLowerCase()] ?? "application/octet-stream",
    "content-length": info.size,
  });
  if (req.method === "HEAD") {
    res.end();
    return;
  }
  createReadStream(filePath)
    .on("error", () => res.destroy())
    .pipe(res);
}

const server = createServer(
  {
    cert: readFileSync(join(TLS_DIR, "tls.crt")),
    key: readFileSync(join(TLS_DIR, "tls.key")),
  },
  (req, res) => {
    handle(req, res).catch(() => {
      if (res.headersSent) {
        res.destroy();
      } else {
        notFound(res);
      }
    });
  },
);

server.listen(PORT, HOST, () => {
  console.log(`Serving ${DIST_DIR} on https://${HOST}:${PORT}`);
});

process.once("SIGTERM", () => {
  server.close(() => process.exit(0));
  server.closeIdleConnections();
});
