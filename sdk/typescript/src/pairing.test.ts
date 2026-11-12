import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  CredentialError,
  noCredentialMessage,
  PAIR_EXCHANGE_PATH,
  pairUrlFromBase,
  plugin,
  resolveCredential,
  serve,
  TUNNEL_PATH,
  tunnelUrlFromBase,
  writeSavedCredential,
} from "./index.ts";

function tempEnv<T>(vars: Record<string, string | undefined>, fn: () => Promise<T>): Promise<T> {
  const saved: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(vars)) {
    saved[k] = process.env[k];
    if (v === undefined) {
      delete process.env[k];
    } else {
      process.env[k] = v;
    }
  }
  return fn().finally(() => {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) {
        delete process.env[k];
      } else {
        process.env[k] = v;
      }
    }
  });
}

function fakeEther(handler: (req: http.IncomingMessage, res: http.ServerResponse) => void) {
  const server = http.createServer(handler);
  return new Promise<{ url: string; close: () => Promise<void>; calls: string[] }>((resolve) => {
    const calls: string[] = [];
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      resolve({
        url: `http://127.0.0.1:${port}`,
        calls,
        close: () => new Promise((r) => server.close(() => r())),
      });
    });
  });
}

test("pairing exchange saves the credential file with 0600", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ether-pair-"));
  const codes: string[] = [];
  const ether = await fakeEther((req, res) => {
    codes.push(new URL(req.url ?? "/", "http://x").pathname);
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      const parsed = JSON.parse(body || "{}") as { code?: string };
      codes.push(parsed.code ?? "");
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ credential: "cred-1", pluginId: "p" }));
    });
  });
  try {
    const out = await resolveCredential("p", { url: ether.url, code: "CODE-1", stateDir: dir });
    assert.equal(out.credential, "cred-1");
    assert.equal(out.fromFile, true);
    assert.deepEqual(codes, [PAIR_EXCHANGE_PATH, "CODE-1"]);
    const file = path.join(dir, "credential");
    assert.equal(fs.statSync(file).mode & 0o777, 0o600);
    assert.equal(fs.statSync(dir).mode & 0o777, 0o700);
  } finally {
    await ether.close();
  }
});

test("restart uses the saved file without a code", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ether-pair-"));
  let exchanges = 0;
  const ether = await fakeEther((_req, res) => {
    exchanges++;
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ credential: "cred-1" }));
  });
  try {
    await resolveCredential("p", { url: ether.url, code: "CODE-1", stateDir: dir });
    const out = await resolveCredential("p", { stateDir: dir });
    assert.equal(out.credential, "cred-1");
    assert.equal(out.fromFile, true);
    assert.equal(exchanges, 1);
  } finally {
    await ether.close();
  }
});

test("an explicit credential wins over the saved file", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ether-pair-"));
  await writeSavedCredential(dir, "saved", "http://example.test");
  const out = await resolveCredential("p", {
    credential: "explicit",
    url: "http://example.test",
    code: "CODE-1",
    stateDir: dir,
  });
  assert.equal(out.credential, "explicit");
  assert.equal(out.fromFile, false);
});

test("no credential gives the clear line", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ether-pair-"));
  await tempEnv({ ETHER_PLUGIN_CREDENTIAL: undefined }, async () => {
    await assert.rejects(
      resolveCredential("p", { stateDir: dir }),
      (err: unknown) => err instanceof CredentialError && err.message === noCredentialMessage(),
    );
  });
});

test("a 401 from the tunnel clears the saved credential", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ether-pair-"));
  const ether = await fakeEther((_req, res) => {
    res.writeHead(401, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "unauthorized" }));
  });
  try {
    await writeSavedCredential(dir, "stale", ether.url);
    const instance = plugin({ manifest: { id: "p", name: "p", version: "1" } });
    await assert.rejects(
      serve(instance, { mode: "tunnel", tunnelUrl: `ws${ether.url.slice(4)}${TUNNEL_PATH}`, stateDir: dir }),
      CredentialError,
    );
    assert.equal(fs.existsSync(path.join(dir, "credential")), false);
  } finally {
    await ether.close();
  }
});

test("tunnel URL derivation", () => {
  assert.equal(tunnelUrlFromBase("http://host:32450"), `ws://host:32450${TUNNEL_PATH}`);
  assert.equal(tunnelUrlFromBase("https://host/"), `wss://host${TUNNEL_PATH}`);
  assert.equal(pairUrlFromBase("https://host"), `https://host${PAIR_EXCHANGE_PATH}`);
  assert.throws(() => tunnelUrlFromBase("ftp://host"), CredentialError);
});
