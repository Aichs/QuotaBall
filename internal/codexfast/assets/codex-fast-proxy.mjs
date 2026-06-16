import http from "node:http";
import { appendFile } from "node:fs/promises";
import { Readable } from "node:stream";

const port = Number(process.env.CODEX_FAST_PROXY_PORT || "48251");
const host = process.env.CODEX_FAST_PROXY_HOST || "127.0.0.1";
const targetOrigin = (process.env.CODEX_FAST_PROXY_TARGET_ORIGIN || "https://api.krill-ai.com").replace(/\/+$/, "");
const fastModels = new Set(
  (process.env.CODEX_FAST_PROXY_MODELS || "gpt-5.5,gpt-5.4")
    .split(",")
    .map((model) => model.trim())
    .filter(Boolean),
);
const logFile = process.env.CODEX_FAST_PROXY_LOG || new URL("./fast-proxy.log", import.meta.url).pathname;

const hopByHopHeaders = new Set([
  "connection",
  "content-encoding",
  "content-length",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
]);

function now() {
  return new Date().toISOString();
}

async function log(entry) {
  const safe = {
    time: now(),
    method: entry.method,
    path: entry.path,
    model: entry.model,
    action: entry.action,
    status: entry.status,
    error: entry.error,
  };
  await appendFile(logFile, `${JSON.stringify(safe)}\n`, "utf8").catch(() => {});
}

function collectBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (chunk) => chunks.push(chunk));
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

function shouldRewriteJson(req, body) {
  const method = (req.method || "").toUpperCase();
  if (method !== "POST") {
    return false;
  }
  const contentType = String(req.headers["content-type"] || "");
  if (!contentType.includes("application/json")) {
    return false;
  }
  const pathname = new URL(req.url || "/", `http://${host}:${port}`).pathname;
  return pathname.endsWith("/responses") && body.length > 0;
}

function maybeInjectFast(req, body) {
  if (!shouldRewriteJson(req, body)) {
    return { body, action: "skip", model: undefined };
  }

  let payload;
  try {
    payload = JSON.parse(body.toString("utf8"));
  } catch {
    return { body, action: "invalid-json", model: undefined };
  }

  const model = typeof payload.model === "string" ? payload.model : undefined;
  if (!model || !fastModels.has(model)) {
    return { body, action: "skip-model", model };
  }

  if (payload.service_tier && payload.service_tier !== "default") {
    return { body, action: "preserve", model };
  }

  payload.service_tier = "priority";
  return {
    body: Buffer.from(JSON.stringify(payload), "utf8"),
    action: "inject-priority",
    model,
  };
}

function buildForwardHeaders(req, body) {
  const headers = {};
  for (const [key, value] of Object.entries(req.headers)) {
    const lower = key.toLowerCase();
    if (!hopByHopHeaders.has(lower)) {
      headers[key] = value;
    }
  }
  headers.host = new URL(targetOrigin).host;
  if (body) {
    headers["content-length"] = String(body.length);
  }
  return headers;
}

function sendJson(res, status, payload) {
  const body = Buffer.from(JSON.stringify(payload), "utf8");
  res.writeHead(status, {
    "content-type": "application/json",
    "content-length": String(body.length),
  });
  res.end(body);
}

const server = http.createServer(async (req, res) => {
  const requestUrl = new URL(req.url || "/", `http://${host}:${port}`);

  if (requestUrl.pathname === "/_codex_fast_proxy/health") {
    sendJson(res, 200, {
      ok: true,
      targetOrigin,
      fastModels: Array.from(fastModels),
    });
    return;
  }

  if (requestUrl.pathname === "/_codex_fast_proxy/preview") {
    const original = await collectBody(req);
    const transformed = maybeInjectFast(
      {
        method: "POST",
        url: "/codex/v1/responses",
        headers: { "content-type": "application/json" },
      },
      original,
    );
    sendJson(res, 200, {
      action: transformed.action,
      model: transformed.model,
      body: JSON.parse(transformed.body.toString("utf8") || "{}"),
    });
    return;
  }

  try {
    const originalBody = await collectBody(req);
    const transformed = maybeInjectFast(req, originalBody);
    const requestBody = transformed.body;

    const targetUrl = `${targetOrigin}${req.url || "/"}`;
    const response = await fetch(targetUrl, {
      method: req.method,
      headers: buildForwardHeaders(req, requestBody),
      body: requestBody.length ? requestBody : undefined,
      redirect: "manual",
    });

    const responseHeaders = {};
    response.headers.forEach((value, key) => {
      if (!hopByHopHeaders.has(key.toLowerCase())) {
        responseHeaders[key] = value;
      }
    });

    res.writeHead(response.status, responseHeaders);
    if (response.body) {
      Readable.fromWeb(response.body).pipe(res);
    } else {
      res.end();
    }

    await log({
      method: req.method,
      path: requestUrl.pathname,
      model: transformed.model,
      action: transformed.action,
      status: response.status,
    });
  } catch (error) {
    await log({
      method: req.method,
      path: requestUrl.pathname,
      action: "error",
      error: error instanceof Error ? error.message : String(error),
    });
    sendJson(res, 502, { error: "codex_fast_proxy_forward_failed" });
  }
});

server.listen(port, host, () => {
  log({ method: "LISTEN", path: `${host}:${port}`, action: "ready" });
});
