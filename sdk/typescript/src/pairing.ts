/**
 * Sidecar pairing built into the SDK. Every plugin used to hand-write the
 * code exchange and keep the credential in memory; this resolves a credential
 * once, saves it under the state directory, and reuses it across restarts.
 */
export const PAIR_EXCHANGE_PATH = "/ether/plugins/pair";
export const TUNNEL_PATH = "/ether/plugins/tunnel";

const CREDENTIAL_FILE_MODE = 0o600;
const STATE_DIR_MODE = 0o700;

export class CredentialError extends Error {}

export function noCredentialMessage(): string {
  return (
    "No credential: create a pairing code in Ether (Plugins > this plugin > " +
    "Connect a sidecar) and set ETHER_PAIRING_CODE."
  );
}

export interface PairingInputs {
  url?: string;
  code?: string;
  credential?: string;
  stateDir?: string;
}

function env(name: string): string | undefined {
  const g = globalThis as { process?: { env?: Record<string, string | undefined> } };
  return g.process?.env?.[name];
}

async function nodeFs(): Promise<typeof import("node:fs")> {
  return (await import("node:fs")) as typeof import("node:fs");
}

async function nodeOs(): Promise<typeof import("node:os")> {
  return (await import("node:os")) as typeof import("node:os");
}

export function serverBase(base: string | undefined): string {
  const trimmed = (base ?? "").trim();
  if (!trimmed) {
    throw new CredentialError("ETHER_URL is not set");
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new CredentialError("ETHER_URL is not a valid URL");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new CredentialError("ETHER_URL must use http or https");
  }
  if (!parsed.host) {
    throw new CredentialError("ETHER_URL has no host");
  }
  return `${parsed.protocol}//${parsed.host}`;
}

export function tunnelUrlFromBase(base: string | undefined): string {
  const parsed = new URL(serverBase(base));
  const scheme = parsed.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${parsed.host}${TUNNEL_PATH}`;
}

export function pairUrlFromBase(base: string | undefined): string {
  return serverBase(base) + PAIR_EXCHANGE_PATH;
}

export async function resolveStateDir(explicit: string, pluginId: string): Promise<string> {
  if (explicit.trim()) {
    return explicit.trim();
  }
  const fs = await nodeFs();
  try {
    const info = fs.statSync("/data");
    if (info.isDirectory()) {
      fs.accessSync("/data", fs.constants.W_OK);
      return "/data";
    }
  } catch {
    // Fall through to the user config directory.
  }
  const os = await nodeOs();
  const configHome = env("XDG_CONFIG_HOME") ?? `${os.homedir()}/.config`;
  return `${configHome}/ether-plugins/${pluginId || "plugin"}`;
}

function credentialPath(stateDir: string): string {
  return `${stateDir}/credential`;
}

export async function readSavedCredential(stateDir: string): Promise<string | undefined> {
  const fs = await nodeFs();
  try {
    const raw = fs.readFileSync(credentialPath(stateDir), "utf8");
    const parsed = JSON.parse(raw) as { credential?: string };
    return parsed.credential || undefined;
  } catch {
    return undefined;
  }
}

export async function writeSavedCredential(
  stateDir: string,
  credential: string,
  url: string,
): Promise<void> {
  const fs = await nodeFs();
  fs.mkdirSync(stateDir, { recursive: true, mode: STATE_DIR_MODE });
  try {
    fs.chmodSync(stateDir, STATE_DIR_MODE);
  } catch {
    // Best effort: some filesystems ignore modes.
  }
  const path = credentialPath(stateDir);
  fs.writeFileSync(path, JSON.stringify({ credential, url }), { mode: CREDENTIAL_FILE_MODE });
  try {
    fs.chmodSync(path, CREDENTIAL_FILE_MODE);
  } catch {
    // Best effort.
  }
}

export async function clearSavedCredential(stateDir: string): Promise<void> {
  const fs = await nodeFs();
  try {
    fs.unlinkSync(credentialPath(stateDir));
  } catch {
    // Already gone.
  }
}

async function exchangeCode(base: string, code: string): Promise<string> {
  const res = await fetch(pairUrlFromBase(base), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!res.ok) {
    throw new CredentialError(`Pairing code was rejected (HTTP ${res.status}).`);
  }
  const payload = (await res.json()) as { credential?: string };
  if (!payload.credential) {
    throw new CredentialError("Pairing response had no credential.");
  }
  return payload.credential;
}

/**
 * Applies the startup order: explicit credential, then the saved file, then a
 * one-time pairing code (exchanged and saved). Returns the credential, whether
 * it came from the file, and the state directory. Never logs either secret.
 */
export async function resolveCredential(
  pluginId: string,
  inputs: PairingInputs,
): Promise<{ credential: string; fromFile: boolean; stateDir: string }> {
  const stateDir = await resolveStateDir(
    inputs.stateDir ?? env("ETHER_PLUGIN_STATE_DIR") ?? "",
    pluginId,
  );
  const explicit = (inputs.credential ?? env("ETHER_PLUGIN_CREDENTIAL") ?? "").trim();
  if (explicit) {
    return { credential: explicit, fromFile: false, stateDir };
  }
  const saved = await readSavedCredential(stateDir);
  if (saved) {
    return { credential: saved, fromFile: true, stateDir };
  }
  const url = inputs.url ?? env("ETHER_URL") ?? "";
  const code = (inputs.code ?? env("ETHER_PAIRING_CODE") ?? "").trim();
  if (url.trim() && code) {
    const credential = await exchangeCode(url, code);
    await writeSavedCredential(stateDir, credential, url);
    return { credential, fromFile: true, stateDir };
  }
  throw new CredentialError(noCredentialMessage());
}
