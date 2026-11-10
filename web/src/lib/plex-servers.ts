import { t } from "../i18n";
import { apiFetch } from "./api";

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
}

export interface PMSServer {
  id: number;
  name: string;
  url: string;
  machineId: string;
  version: string;
  owner: string;
  stepAside?: boolean;
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
