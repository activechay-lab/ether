import { t, tn } from "../i18n";
import { apiFetch } from "./api";

export interface PMSBackfillStatus {
  running: boolean;
  processed: number;
  total: number;
  doneAt: number;
}

export interface PMSBackfillPreview {
  remoteViews: number;
  remoteKnown: boolean;
  localWatched: number;
  localInProgress: number;
}

export interface PMSMapping {
  accountId: number;
  accountName: string;
  owner: boolean;
  userId: number;
  direction: number;
  interval: number;
  canSend: boolean;
  lastImport: number;
  lastSend: number;
  nextSync: number;
  problem: string;
  unmatched: number;
  inBackfill?: PMSBackfillStatus;
  outBackfill?: PMSBackfillStatus;
}

export interface PMSStepAsideStatus {
  state: string;
  movies: number;
  showsFull: number;
  showsPartial: number;
  episodes: number;
  unmatched: number;
  lastSynced: number;
  lastScan: number;
  problem: string;
}

export interface PMSServer {
  id: number;
  name: string;
  url: string;
  machineId: string;
  version: string;
  owner: string;
  stepAside?: boolean;
  stepAsideStatus?: PMSStepAsideStatus | null;
  checkedAt: number;
  problem: string;
  viewers: PMSMapping[];
}

export interface PMSViewerMapping {
  serverId: number;
  serverName: string;
  accountId: number;
  accountName: string;
  owner: boolean;
  direction: number;
  interval: number;
  lastImport: number;
  lastSend: number;
  nextSync: number;
  problem: string;
  unmatched: number;
  inBackfill?: PMSBackfillStatus;
  outBackfill?: PMSBackfillStatus;
}

export function pmsProblemText(code: string): string {
  switch (code) {
    case "token":
      return t("The server refused its token. Edit the server and paste a new token.");
    case "account_token":
      return t("The server refused this person's token. Paste a new one.");
    case "unreachable":
      return t("Ether can't reach this server. Check that it's running and the address is right.");
    case "rate_limited":
      return t("The server asked Ether to slow down. Sync resumes on its own.");
    case "server":
      return t("The server reported an error. Ether will try again.");
    case "forbidden":
      return t("The server didn't allow this. Check that the token is the server owner's.");
    case "":
      return "";
    default:
      return t("Sync stopped on an unexpected error. Ether will try again.");
  }
}

export function pmsDirectionText(direction: number): string {
  switch (direction) {
    case 1:
      return t("Ether → Plex");
    case 2:
      return t("Plex → Ether");
    case 3:
      return t("Both");
    default:
      return t("Off");
  }
}

export function pmsBackfillLines(
  m: Pick<PMSMapping, "direction" | "inBackfill" | "outBackfill">,
  formatWhen: (seconds: number) => string,
): string[] {
  const lines: string[] = [];
  if ((m.direction & 2) !== 0 && m.inBackfill) {
    const b = m.inBackfill;
    if (b.running) {
      if (b.total > 0) {
        lines.push(
          t("Full history from this server: {processed} of {total} read…", {
            processed: b.processed,
            total: b.total,
          }),
        );
      } else if (b.processed > 0) {
        lines.push(
          tn(
            b.processed,
            "Full history from this server: {count} entry read…",
            "Full history from this server: {count} entries read…",
          ),
        );
      } else {
        lines.push(t("Full history from this server: queued…"));
      }
    } else if (b.doneAt > 0) {
      lines.push(t("Full history from this server: done ({when})", { when: formatWhen(b.doneAt) }));
    }
  }
  if ((m.direction & 1) !== 0 && m.outBackfill) {
    const b = m.outBackfill;
    if (b.running) {
      if (b.total > 0) {
        lines.push(
          t("Full history to this server: {processed} of {total} checked…", {
            processed: b.processed,
            total: b.total,
          }),
        );
      } else {
        lines.push(t("Full history to this server: queued…"));
      }
    } else if (b.doneAt > 0) {
      lines.push(t("Full history to this server: done ({when})", { when: formatWhen(b.doneAt) }));
    }
  }
  return lines;
}

/** The single registry of owner-run Plex servers, shared by sync and one-time import. */
export const listPlexServers = (signal?: AbortSignal) =>
  apiFetch<PMSServer[]>("/plex-servers", { signal });

/** Update whether Ether hides titles already held by this Plex Media Server from Plex apps. */
export const setPlexServerStepAside = (id: number, stepAside: boolean) =>
  apiFetch<PMSServer>(`/plex-servers/${id}/step-aside`, {
    method: "PUT",
    body: JSON.stringify({ stepAside }),
  });

/** Read-only view of one viewer's owner-run Plex Media Server mappings. */
export const listViewerPlexServers = (viewerId: number, signal?: AbortSignal) =>
  apiFetch<PMSViewerMapping[]>(`/viewers/${viewerId}/plex-servers`, { signal });

export const getPlexServerBackfillPreview = (
  serverId: number,
  accountId: number,
  signal?: AbortSignal,
) =>
  apiFetch<PMSBackfillPreview>(`/plex-servers/${serverId}/accounts/${accountId}/backfill`, {
    signal,
  });

export const startPlexServerBackfill = (serverId: number, accountId: number, kind: "in" | "out") =>
  apiFetch<PMSServer>(
    `/plex-servers/${serverId}/accounts/${accountId}/${kind === "in" ? "backfill-in" : "backfill-out"}`,
    { method: "POST" },
  );

export interface PMSStepAsideExplanation {
  key: number;
  kind: string;
  title: string;
  hidden: boolean;
  cause: string;
  servers: string[];
  airedRegular: number;
  heldEpisodes: number;
  missing: string[];
  missingMore: number;
  scanState: string;
}

/** Explain why one movie or show is hidden or still showing. */
export const explainPlexServerStepAside = (key: number, signal?: AbortSignal) =>
  apiFetch<PMSStepAsideExplanation>(`/plex-servers/step-aside/explain?key=${key}`, { signal });

/** One plain-language line for a server's step-aside scan state. */
export function pmsStepAsideStateText(state: string): string {
  switch (state) {
    case "complete":
      return t("Up to date");
    case "matching":
      return t("Still matching titles…");
    case "failed":
      return t("The last check failed. Ether will try again.");
    case "never":
      return t("Waiting for the first check…");
    default:
      return "";
  }
}
