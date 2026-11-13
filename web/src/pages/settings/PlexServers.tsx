import {
  Badge,
  Button,
  Input,
  Select,
  SettingsSection,
  Spinner,
  Switch,
  toast,
} from "@braedonsaunders/appkit-ui";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, MutationError } from "../../components/feedback";
import { Field, SecretInput } from "../../components/fields";
import { IntegrationAppCard } from "../../components/IntegrationAppCard";
import { PlexMark } from "../../components/integration-brands";
import { TitleSearch } from "../../components/pickers";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tn, tx } from "../../i18n";
import { formatDateTime } from "../../lib/format";
import { api, apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";

import {
  getPlexServerBackfillPreview,
  explainPlexServerStepAside,
  listPlexServers,
  pmsBackfillLines,
  pmsDirectionText,
  pmsProblemText,
  pmsStepAsideStateText,
  setPlexServerStepAside,
  startPlexServerBackfill,
  type PMSBackfillPreview,
  type PMSMapping,
  type PMSServer,
  type PMSStepAsideExplanation,
} from "../../lib/plex-servers";

interface PMSAccount {
  id: number;
  name: string;
  owner: boolean;
  userId: number;
}

interface PMSUnmatched {
  direction: string;
  ref: string;
  title: string;
  reason: string;
  seenAt: number;
}

type ServerDraft = { id?: number; name: string; url: string; token: string };
type MappingDraft = {
  server: PMSServer;
  account: PMSAccount;
  userId: number;
  direction: number;
  interval: number;
  token: string;
  hasToken: boolean;
};
type BackfillDraft = {
  kind: "in" | "out";
  server: PMSServer;
  account: PMSAccount;
  mapping: PMSMapping;
  preview: PMSBackfillPreview | null;
  loading: boolean;
};

const INTERVALS = [5, 10, 30, 60];

function reasonText(code: string): string {
  switch (code) {
    case "no_match":
      return t("No TMDB, IMDb or TVDB match");
    case "kind":
      return t("Not a movie or episode");
    case "not_on_server":
      return t("Not on this server");
    case "no_plex_id":
      return t("No Plex identity yet");
    default:
      return t("Not matched");
  }
}

function errorText(e: unknown): string {
  const code = (e as { code?: string })?.code;
  switch (code) {
    case "token":
      return t("The server refused this token.");
    case "not_owner":
      return t("This token works, but it isn't the server owner's.");
    case "unreachable":
      return t("Ether can't reach this server. Check that it's running and the address is right.");
    case "duplicate":
      return t("This server, or this viewer, is already set up here.");
    case "different_server":
      return t("That address is a different Plex Media Server. Add it as a new server instead.");
    case "needs_token":
      return t("Sending to Plex for this person needs their own token for this server.");
    case "receiving_off":
      return t("Reading from Plex is turned off for this person.");
    case "sending_off":
      return t("Sending to Plex is turned off for this person.");
    case "address":
      return t("Enter the server address, like http://192.168.1.10:32400");
    case "limit":
      return t("You've reached the limit for Plex Media Servers.");
    default:
      return t("Plex Media Server settings were not saved.");
  }
}

const when = (seconds: number) => (seconds ? formatDateTime(new Date(seconds * 1000)) : t("Never"));

/** One plain-language explanation of a step-aside answer, for a non-technical owner. */
function stepAsideCauseText(a: PMSStepAsideExplanation): string {
  switch (a.cause) {
    case "held":
      return a.kind === "movie"
        ? t("Hidden on Plex apps: {server} holds this movie.", {
            server: a.servers.join(", ") || t("your server"),
          })
        : t("Hidden on Plex apps: {server} has every aired episode of this show.", {
            server: a.servers.join(", ") || t("your server"),
          });
    case "missing_episodes":
      return t(
        "Still showing: {server} is missing {count} aired {episodes} of this show, so Ether keeps the whole show for them.",
        {
          server: a.servers.join(", ") || t("your server"),
          count: a.missing.length + a.missingMore,
          episodes: a.missing.length + a.missingMore === 1 ? t("episode") : t("episodes"),
        },
      );
    case "no_episode_list":
      return t(
        "Still showing: Ether has not loaded this show's episode list yet, so it cannot tell whether your server has them all. It will happen on its own — no action needed.",
      );
    case "not_held":
      return t("Still showing: no step-aside server holds this title yet.");
    case "scan_capped":
      return t(
        "Still showing: Ether is still matching your server's library. Large libraries take a few minutes; this page will update on its own.",
      );
    case "scan_failed":
      return t(
        "Still showing: the last check of this server failed, so Ether isn't sure what it holds. It will try again.",
      );
    case "not_scanned":
      return t(
        "Still showing: this server has not been checked yet. The first check runs shortly.",
      );
    case "step_aside_off":
      return t(
        "Still showing: step aside is off for every server. Turn it on above to hide titles your server already has.",
      );
    case "not_found":
      return t("Ether doesn't know this title.");
    default:
      return t("Still showing: Ether has no explanation yet.");
  }
}

/** Uses the overview persistence queue and global switch lock. */
export function PlexServersCard({
  enabled,
  busy,
  disabled,
  onToggle,
}: {
  enabled: boolean;
  busy: boolean;
  disabled: boolean;
  onToggle: (next: boolean) => void;
}) {
  const { pathname } = useLocation();
  const servers = useAsync((signal) => listPlexServers(signal), [pathname]);
  const list = servers.data ?? [];
  const attention = list.some((s) => s.problem || s.viewers.some((v) => v.problem));
  return (
    <IntegrationAppCard
      identity={{ id: "plexservers", name: t("Plex Media Server Sync") }}
      logo={<PlexMark />}
      description={t("Keeps watch state in step with your own Plex Media Servers, per viewer.")}
      enabled={enabled}
      busy={busy || servers.loading}
      disabled={disabled || !!servers.error}
      onToggle={onToggle}
      status={
        !enabled ? (
          <Badge variant="secondary">{t("Off")}</Badge>
        ) : servers.error ? (
          <Badge variant="warning">{t("Needs attention")}</Badge>
        ) : list.length === 0 ? (
          <Badge variant="secondary">{t("Not set up")}</Badge>
        ) : attention ? (
          <Badge variant="warning">{t("Needs attention")}</Badge>
        ) : (
          <Badge variant="success">{t("On")}</Badge>
        )
      }
      configureHref="/services/plexservers"
      configureLabel={
        list.length ? t("Manage Plex Media Server Sync") : t("Set up Plex Media Server Sync")
      }
    />
  );
}

/** Integrations › Plex Media Servers: server-to-server watch sync. */
export function PlexServers({ enabled }: { enabled: boolean }) {
  const { pathname } = useLocation();
  const servers = useAsync((signal) => listPlexServers(signal), [pathname]);
  const viewers = useAsync(() => api.viewers(), []);
  const [server, setServer] = useState<ServerDraft | null>(null);
  const [mapping, setMapping] = useState<MappingDraft | null>(null);
  const [backfill, setBackfill] = useState<BackfillDraft | null>(null);
  const [accounts, setAccounts] = useState<Record<number, PMSAccount[] | "error">>({});
  const [unmatched, setUnmatched] = useState<{
    server: PMSServer;
    viewer: number;
    items: PMSUnmatched[];
    more: boolean;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [explain, setExplain] = useState<{
    server: PMSServer;
    answer: PMSStepAsideExplanation | null;
    loading: boolean;
  } | null>(null);
  const previewAbort = useRef<AbortController | null>(null);

  const replace = (next: PMSServer) =>
    servers.setData((servers.data ?? []).map((s) => (s.id === next.id ? next : s)));
  const viewerName = (id: number) => {
    const v = (viewers.data ?? []).find((x) => x.id === id);
    return v ? v.title || v.username : t("Unknown viewer");
  };
  const viewerLink = (id: number) => {
    const v = (viewers.data ?? []).find((x) => x.id === id);
    if (!v) return t("Unknown viewer");
    return (
      <Link to={`/viewers/${v.id}/limits`} className="font-medium text-primary hover:underline">
        {v.title || v.username}
      </Link>
    );
  };

  const loadAccounts = async (srv: PMSServer) => {
    try {
      const list = await apiFetch<PMSAccount[]>(`/plex-servers/${srv.id}/accounts`);
      setAccounts((old) => ({ ...old, [srv.id]: list }));
    } catch {
      setAccounts((old) => ({ ...old, [srv.id]: "error" }));
    }
  };

  const saveServer = async (draft: ServerDraft) => {
    setBusy(true);
    setError(null);
    try {
      const body = JSON.stringify({ name: draft.name, url: draft.url, token: draft.token });
      const next = draft.id
        ? await apiFetch<PMSServer>(`/plex-servers/${draft.id}`, { method: "PUT", body })
        : await apiFetch<PMSServer>("/plex-servers", { method: "POST", body });
      if (draft.id) replace(next);
      else servers.setData([...(servers.data ?? []), next]);
      setServer(null);
      void loadAccounts(next);
      toast.success(t("Plex Media Server saved"));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const removeServer = async (id: number) => {
    if (!window.confirm(t("Remove this server? Watched marks already synced stay where they are.")))
      return;
    setBusy(true);
    try {
      await apiFetch(`/plex-servers/${id}`, { method: "DELETE" });
      servers.setData((servers.data ?? []).filter((s) => s.id !== id));
      setServer(null);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const saveMapping = async (draft: MappingDraft, userId = draft.userId) => {
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        userId,
        direction: draft.direction,
        interval: draft.interval,
      };
      if (draft.token) body.token = draft.token;
      const next = await apiFetch<PMSServer>(
        `/plex-servers/${draft.server.id}/accounts/${draft.account.id}`,
        { method: "PUT", body: JSON.stringify(body) },
      );
      replace(next);
      setAccounts((old) => {
        const list = old[draft.server.id];
        if (!Array.isArray(list)) return old;
        return {
          ...old,
          [draft.server.id]: list.map((a) => (a.id === draft.account.id ? { ...a, userId } : a)),
        };
      });
      setMapping(null);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const syncNow = async (srv: PMSServer) => {
    setBusy(true);
    try {
      replace(await apiFetch<PMSServer>(`/plex-servers/${srv.id}/sync`, { method: "POST" }));
    } catch {
      toast.error(t("Plex Media Server Sync didn't run"));
    } finally {
      setBusy(false);
    }
  };

  const closeBackfill = () => {
    previewAbort.current?.abort();
    previewAbort.current = null;
    setBackfill(null);
  };

  const openBackfill = (kind: "in" | "out", srv: PMSServer, account: PMSAccount, m: PMSMapping) => {
    previewAbort.current?.abort();
    const ctrl = new AbortController();
    previewAbort.current = ctrl;
    setError(null);
    setBackfill({ kind, server: srv, account, mapping: m, preview: null, loading: true });
    void getPlexServerBackfillPreview(srv.id, account.id, ctrl.signal)
      .then((preview) => {
        if (ctrl.signal.aborted) return;
        setBackfill((cur) =>
          cur && cur.server.id === srv.id && cur.account.id === account.id && cur.kind === kind
            ? { ...cur, preview, loading: false }
            : cur,
        );
      })
      .catch(() => {
        if (ctrl.signal.aborted) return;
        setBackfill((cur) =>
          cur && cur.server.id === srv.id && cur.account.id === account.id && cur.kind === kind
            ? { ...cur, loading: false }
            : cur,
        );
      });
  };

  const confirmBackfill = async (draft: BackfillDraft) => {
    setBusy(true);
    setError(null);
    try {
      const next = await startPlexServerBackfill(draft.server.id, draft.account.id, draft.kind);
      replace(next);
      closeBackfill();
      toast.success(
        draft.kind === "in" ? t("Full history import started") : t("Full history send started"),
      );
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const toggleStepAside = async (srv: PMSServer, next: boolean) => {
    setBusy(true);
    try {
      replace(await setPlexServerStepAside(srv.id, next));
    } catch {
      toast.error(t("Plex Media Server settings were not saved."));
    } finally {
      setBusy(false);
    }
  };

  const openExplain = (srv: PMSServer, key: number) => {
    const ctrl = new AbortController();
    previewAbort.current?.abort();
    previewAbort.current = ctrl;
    setExplain({ server: srv, answer: null, loading: true });
    void explainPlexServerStepAside(key, ctrl.signal)
      .then((answer) => {
        if (ctrl.signal.aborted) return;
        setExplain((cur) => (cur ? { ...cur, answer, loading: false } : cur));
      })
      .catch(() => {
        if (ctrl.signal.aborted) return;
        setExplain((cur) => (cur ? { ...cur, loading: false } : cur));
      });
  };

  const openUnmatched = async (srv: PMSServer, viewer: number, after = "") => {
    setBusy(true);
    try {
      const items = await apiFetch<PMSUnmatched[]>(
        `/plex-servers/${srv.id}/unmatched?viewer=${viewer}&after=${encodeURIComponent(after)}`,
      );
      setUnmatched((old) => ({
        server: srv,
        viewer,
        items: after && old ? [...old.items, ...items] : items,
        more: items.length === 100,
      }));
    } catch {
      toast.error(t("Unmatched titles could not be loaded"));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => () => previewAbort.current?.abort(), []);

  // Each server's people are listed live from that server, once per visit.
  const serverIDs = (servers.data ?? []).map((s) => s.id).join(",");
  useEffect(() => {
    for (const srv of servers.data ?? []) {
      if (accounts[srv.id] === undefined) void loadAccounts(srv);
    }
    // loadAccounts only sets state; re-running on accounts would refetch failures.
  }, [serverIDs]);

  const anyBackfillRunning = (servers.data ?? []).some((s) =>
    s.viewers.some((v) => v.inBackfill?.running || v.outBackfill?.running),
  );
  useEffect(() => {
    if (!anyBackfillRunning) return;
    let ctrl: AbortController | null = null;
    const timer = setInterval(() => {
      ctrl?.abort();
      ctrl = new AbortController();
      void listPlexServers(ctrl.signal)
        .then((next) => servers.setData(next))
        .catch(() => {});
    }, 3000);
    return () => {
      clearInterval(timer);
      ctrl?.abort();
    };
  }, [anyBackfillRunning]);

  const openMapping = (srv: PMSServer, account: PMSAccount) => {
    const m = srv.viewers.find((v) => v.accountId === account.id);
    setError(null);
    setMapping({
      server: srv,
      account,
      userId: m?.userId ?? 0,
      direction: m?.direction ?? (account.owner ? 3 : 2),
      interval: m?.interval ?? 10,
      token: "",
      hasToken: !!m && m.canSend && !account.owner,
    });
  };

  if (servers.error)
    return (
      <ErrorPanel
        error={new Error(t("Plex Media Servers could not be loaded"))}
        onRetry={() => void servers.reload()}
      />
    );
  if (!servers.data)
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading Plex Media Servers")} />
      </div>
    );

  const list = servers.data;
  const canSend = mapping ? mapping.account.owner || mapping.hasToken || !!mapping.token : false;
  const backfillAccountName = backfill
    ? backfill.account.name || t("Plex account {id}", { id: backfill.account.id })
    : "";
  return (
    <div className="space-y-5">
      <SettingsSection
        description={t(
          "Add a Plex Media Server you run, then choose which of its people match which viewers. Watched and unwatched marks, and resume points sent to Plex, follow each person's direction. Nothing syncs for people you don't match.",
        )}
        footer={
          <Button
            size="sm"
            disabled={busy}
            onClick={() => {
              setError(null);
              setServer({ name: "", url: "", token: "" });
            }}
          >
            {t("Add server")}
          </Button>
        }
      >
        {list.length === 0 ? (
          <p className="px-5 py-4 text-sm text-fg-muted">{t("No Plex Media Servers yet.")}</p>
        ) : null}
      </SettingsSection>
      {list.map((srv) => {
        const accts = accounts[srv.id];
        return (
          <SettingsSection key={srv.id}>
            <h2 className="flex flex-wrap items-center gap-2 px-5 py-4 text-sm font-semibold">
              {srv.name}
              {srv.problem ? (
                <Badge variant="warning">{t("Needs attention")}</Badge>
              ) : (
                <Badge variant="success">{t("Connected")}</Badge>
              )}
              <span className="font-normal text-fg-muted">
                {srv.url}
                {srv.version ? ` · ${srv.version}` : ""}
              </span>
              <span className="ms-auto flex gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || !enabled}
                  onClick={() => void syncNow(srv)}
                >
                  {t("Sync now")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() => {
                    setError(null);
                    setServer({ id: srv.id, name: srv.name, url: srv.url, token: "" });
                  }}
                >
                  {t("Edit")}
                </Button>
              </span>
            </h2>
            {srv.problem ? (
              <p className="px-5 pb-3 text-sm text-warning">{pmsProblemText(srv.problem)}</p>
            ) : null}
            <SettingsRow
              title={t("Step aside for titles on this server")}
              description={
                <span className="space-y-0.5">
                  <span className="block">
                    {t(
                      "Hide movies, complete shows, and episodes this server already has from Plex apps.",
                    )}
                  </span>
                  {srv.stepAside && srv.stepAsideStatus ? (
                    <span className="block">
                      {t(
                        "{state} · {movies} movies, {shows} shows, {episodes} episodes stepped aside · last checked {when}",
                        {
                          state: pmsStepAsideStateText(srv.stepAsideStatus.state),
                          movies: srv.stepAsideStatus.movies,
                          shows: srv.stepAsideStatus.showsFull + srv.stepAsideStatus.showsPartial,
                          episodes: srv.stepAsideStatus.episodes,
                          when: when(srv.stepAsideStatus.lastScan),
                        },
                      )}
                    </span>
                  ) : null}
                  {srv.stepAside && srv.stepAsideStatus?.unmatched ? (
                    <span className="block">
                      {tn(
                        srv.stepAsideStatus.unmatched,
                        "{count} title on this server didn't match Ether and was skipped.",
                        "{count} titles on this server didn't match Ether and were skipped.",
                      )}
                    </span>
                  ) : null}
                </span>
              }
              control={
                <span className="flex shrink-0 items-center gap-3">
                  {srv.stepAside ? (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy}
                      onClick={() => setExplain({ server: srv, answer: null, loading: false })}
                    >
                      {t("Why is a title still showing?")}
                    </Button>
                  ) : null}
                  <Switch
                    aria-label={t("Step aside for titles on this server")}
                    checked={Boolean(srv.stepAside)}
                    disabled={busy}
                    onChange={(e) => void toggleStepAside(srv, e.currentTarget.checked)}
                  />
                </span>
              }
            />
            {accts === "error" ? (
              <p className="px-5 pb-4 text-sm text-fg-muted">
                {t("This server's people could not be listed.")}{" "}
                <Button size="sm" variant="ghost" onClick={() => void loadAccounts(srv)}>
                  {t("Try again")}
                </Button>
              </p>
            ) : !accts ? (
              <div className="px-5 pb-4">
                <Spinner size={14} label={t("Loading people")} />
              </div>
            ) : (
              accts.map((account) => {
                const m = srv.viewers.find((v) => v.accountId === account.id);
                const statusLines = m ? pmsBackfillLines(m, when) : [];
                return (
                  <SettingsRow
                    key={account.id}
                    title={
                      <span className="flex items-center gap-2">
                        {account.name || t("Plex account {id}", { id: account.id })}
                        {account.owner ? (
                          <Badge variant="secondary">{t("Server owner")}</Badge>
                        ) : null}
                      </span>
                    }
                    description={
                      m ? (
                        <span className="space-y-0.5">
                          <span className="block">
                            {tx("{viewer} · {direction} · every {interval}", {
                              viewer: viewerLink(m.userId),
                              direction: pmsDirectionText(m.direction),
                              interval: tn(m.interval, "{count} minute", "{count} minutes"),
                            })}
                          </span>
                          <span className="block">
                            {t("Last read {last} · Last sent {sent} · Next {next}", {
                              last: when(m.lastImport),
                              sent: when(m.lastSend),
                              next: m.direction ? when(m.nextSync) : t("Off"),
                            })}
                          </span>
                          {statusLines.map((line) => (
                            <span key={line} className="block">
                              {line}
                            </span>
                          ))}
                          {m.problem ? (
                            <span className="block text-warning">{pmsProblemText(m.problem)}</span>
                          ) : null}
                          {m.direction & 3 ? (
                            <span className="flex flex-wrap gap-2 pt-2">
                              {(m.direction & 2) !== 0 ? (
                                <Button
                                  size="sm"
                                  variant="outline"
                                  disabled={busy || !enabled}
                                  onClick={() => openBackfill("in", srv, account, m)}
                                >
                                  {t("Bring in full history from this server")}
                                </Button>
                              ) : null}
                              {(m.direction & 1) !== 0 ? (
                                <Button
                                  size="sm"
                                  variant="outline"
                                  disabled={busy || !enabled || !m.canSend}
                                  onClick={() => openBackfill("out", srv, account, m)}
                                >
                                  {t("Send Ether's full history to this server")}
                                </Button>
                              ) : null}
                            </span>
                          ) : null}
                        </span>
                      ) : (
                        t("Not synced")
                      )
                    }
                    control={
                      <span className="flex shrink-0 flex-wrap justify-end gap-2">
                        {m && m.unmatched > 0 ? (
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={busy}
                            onClick={() => void openUnmatched(srv, m.userId)}
                          >
                            {tn(m.unmatched, "{count} unmatched title", "{count} unmatched titles")}
                          </Button>
                        ) : null}
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy}
                          onClick={() => openMapping(srv, account)}
                        >
                          {m ? t("Change") : t("Match to a viewer")}
                        </Button>
                      </span>
                    }
                  />
                );
              })
            )}
          </SettingsSection>
        );
      })}

      <Drawer
        open={!!server}
        onClose={() => {
          if (!busy) setServer(null);
        }}
        title={server?.id ? t("Edit Plex Media Server") : t("Add server")}
        footer={
          <div className="flex w-full justify-between gap-2">
            {server?.id ? (
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => void removeServer(server.id!)}
              >
                {t("Remove server")}
              </Button>
            ) : (
              <span />
            )}
            <span className="flex gap-2">
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => setServer(null)}>
                {t("Cancel")}
              </Button>
              <Button
                size="sm"
                disabled={busy || !server?.url.trim() || (!server?.id && !server?.token.trim())}
                onClick={() => server && void saveServer(server)}
              >
                {busy ? t("Checking the server…") : t("Save")}
              </Button>
            </span>
          </div>
        }
      >
        {server ? (
          <div className="space-y-4">
            {error ? <MutationError error={new Error(errorText(error))} /> : null}
            <Field label={t("Name")} htmlFor="pms-name">
              <Input
                id="pms-name"
                value={server.name}
                placeholder={t("Living room server")}
                onChange={(e) => setServer({ ...server, name: e.target.value })}
              />
            </Field>
            <Field
              label={t("Address")}
              htmlFor="pms-url"
              hint={t("The address you'd use on your network, with its port.")}
            >
              <Input
                id="pms-url"
                value={server.url}
                placeholder="http://192.168.1.10:32400"
                onChange={(e) => setServer({ ...server, url: e.target.value })}
              />
            </Field>
            <Field
              label={t("Server owner's Plex token")}
              htmlFor="pms-token"
              hint={
                server.id
                  ? t("Leave blank to keep the saved token. Ether never shows it again.")
                  : t(
                      "In Plex Web, open any title's ⋯ menu, choose Get Info, then View XML. The token is the X-Plex-Token at the end of the address. Ether stores it encrypted and never shows it again.",
                    )
              }
            >
              <SecretInput
                id="pms-token"
                value={server.token}
                onChange={(token) => setServer({ ...server, token })}
              />
            </Field>
          </div>
        ) : null}
      </Drawer>

      <Drawer
        open={!!mapping}
        onClose={() => {
          if (!busy) setMapping(null);
        }}
        title={
          mapping
            ? t("Sync {name}", {
                name: mapping.account.name || t("Plex account {id}", { id: mapping.account.id }),
              })
            : ""
        }
        footer={
          <div className="flex w-full justify-between gap-2">
            {mapping && mapping.server.viewers.some((v) => v.accountId === mapping.account.id) ? (
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => mapping && void saveMapping(mapping, 0)}
              >
                {t("Stop syncing")}
              </Button>
            ) : (
              <span />
            )}
            <span className="flex gap-2">
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => setMapping(null)}>
                {t("Cancel")}
              </Button>
              <Button
                size="sm"
                disabled={busy || !mapping?.userId || ((mapping.direction & 1) !== 0 && !canSend)}
                onClick={() => mapping && void saveMapping(mapping)}
              >
                {t("Save")}
              </Button>
            </span>
          </div>
        }
      >
        {mapping ? (
          <div className="space-y-4">
            {error ? <MutationError error={new Error(errorText(error))} /> : null}
            <Field label={t("Ether viewer")} htmlFor="pms-viewer">
              <Select
                id="pms-viewer"
                value={mapping.userId}
                onChange={(e) => setMapping({ ...mapping, userId: Number(e.currentTarget.value) })}
              >
                <option value={0}>{t("Choose a viewer")}</option>
                {(viewers.data ?? []).map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.title || v.username}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label={t("Sync direction")}
              htmlFor="pms-direction"
              hint={t("Turning a direction on starts from now. Past history isn't copied.")}
            >
              <Select
                id="pms-direction"
                value={mapping.direction}
                onChange={(e) =>
                  setMapping({ ...mapping, direction: Number(e.currentTarget.value) })
                }
              >
                {[0, 1, 2, 3].map((d) => (
                  <option key={d} value={d}>
                    {pmsDirectionText(d)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("Check every")} htmlFor="pms-interval">
              <Select
                id="pms-interval"
                value={mapping.interval}
                onChange={(e) =>
                  setMapping({ ...mapping, interval: Number(e.currentTarget.value) })
                }
              >
                {INTERVALS.map((n) => (
                  <option value={n} key={n}>
                    {tn(n, "{count} minute", "{count} minutes")}
                  </option>
                ))}
              </Select>
            </Field>
            {!mapping.account.owner ? (
              <Field
                label={t("This person's Plex token for this server")}
                htmlFor="pms-account-token"
                hint={
                  mapping.hasToken
                    ? t("Saved. Leave blank to keep it.")
                    : t(
                        "Only needed to send to Plex: Plex marks titles watched for whoever's token is used. Reading what they watched works with the owner's token.",
                      )
                }
              >
                <SecretInput
                  id="pms-account-token"
                  value={mapping.token}
                  onChange={(token) => setMapping({ ...mapping, token })}
                />
              </Field>
            ) : null}
          </div>
        ) : null}
      </Drawer>

      <Drawer
        open={!!backfill}
        onClose={() => {
          if (!busy) closeBackfill();
        }}
        title={
          backfill?.kind === "out"
            ? t("Send Ether's full history to this server")
            : t("Bring in full history from this server")
        }
        footer={
          <div className="flex w-full justify-end gap-2">
            <Button variant="ghost" size="sm" disabled={busy} onClick={closeBackfill}>
              {t("Cancel")}
            </Button>
            <Button
              size="sm"
              disabled={busy || !enabled}
              onClick={() => backfill && void confirmBackfill(backfill)}
            >
              {busy
                ? t("Starting…")
                : backfill?.kind === "out"
                  ? t("Start send")
                  : t("Start import")}
            </Button>
          </div>
        }
      >
        {backfill ? (
          <div className="space-y-4 text-sm">
            {error ? <MutationError error={new Error(errorText(error))} /> : null}
            <p>
              {backfill.kind === "in"
                ? t(
                    "Ether will walk {server}'s full watch history for {account} in the background and merge matched movies and episodes into {viewer}'s history. Newer watch state in Ether is never overwritten, and unmatched titles are recorded as usual.",
                    {
                      server: backfill.server.name,
                      account: backfillAccountName,
                      viewer: viewerName(backfill.mapping.userId),
                    },
                  )
                : t(
                    "Ether will walk {viewer}'s watched and in-progress movies and episodes in the background and send matched titles to {server} for {account}. Newer watch state on the Plex server is never overwritten.",
                    {
                      viewer: viewerName(backfill.mapping.userId),
                      server: backfill.server.name,
                      account: backfillAccountName,
                    },
                  )}
            </p>
            <div className="rounded-md border border-border px-3 py-2.5 text-fg-muted">
              {backfill.loading ? (
                <Spinner size={14} label={t("Checking history size")} />
              ) : backfill.preview ? (
                backfill.kind === "in" ? (
                  backfill.preview.remoteKnown ? (
                    tn(
                      backfill.preview.remoteViews,
                      "{count} history entry on this Plex server for this account.",
                      "{count} history entries on this Plex server for this account.",
                    )
                  ) : (
                    t(
                      "History count on this Plex server couldn't be read right now. Ether will still import it in pages once the server answers.",
                    )
                  )
                ) : (
                  t("{watched} and {inProgress} in Ether for this viewer.", {
                    watched: tn(
                      backfill.preview.localWatched,
                      "{count} watched title",
                      "{count} watched titles",
                    ),
                    inProgress: tn(
                      backfill.preview.localInProgress,
                      "{count} in-progress title",
                      "{count} in-progress titles",
                    ),
                  })
                )
              ) : (
                t("Runs in pages in the background without blocking playback or requests.")
              )}
            </div>
          </div>
        ) : null}
      </Drawer>

      <Drawer
        open={!!unmatched}
        onClose={() => setUnmatched(null)}
        title={t("Unmatched titles")}
        description={t(
          "Titles are matched only by TMDB, IMDb or TVDB identity, never by name. These were skipped and are checked again later.",
        )}
      >
        {unmatched ? (
          <div className="space-y-2">
            <ul className="divide-y divide-border text-sm">
              {unmatched.items.map((u) => (
                <li key={`${u.direction}:${u.ref}`} className="py-2">
                  <div className="font-medium">{u.title || u.ref}</div>
                  <div className="text-fg-muted">
                    {u.direction === "in"
                      ? t("From Plex: {reason}", { reason: reasonText(u.reason) })
                      : t("To Plex: {reason}", { reason: reasonText(u.reason) })}
                  </div>
                </li>
              ))}
            </ul>
            {unmatched.more ? (
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => {
                  const last = unmatched.items[unmatched.items.length - 1];
                  if (!last) return;
                  void openUnmatched(
                    unmatched.server,
                    unmatched.viewer,
                    `${last.direction}:${last.ref}`,
                  );
                }}
              >
                {t("Load more")}
              </Button>
            ) : null}
          </div>
        ) : null}
      </Drawer>

      <Drawer
        open={!!explain}
        onClose={() => {
          previewAbort.current?.abort();
          setExplain(null);
        }}
        title={t("Why is a title still showing?")}
        description={t(
          "Pick a movie or show. Ether explains exactly why it is hidden from Plex apps or still showing, using what it knows about {server}.",
          { server: explain?.server.name ?? "" },
        )}
      >
        {explain ? (
          <div className="space-y-4 text-sm">
            <TitleSearch
              action="select"
              includeUnbrowsable
              label={t("Search a movie or show")}
              placeholder={t("Search a movie or show…")}
              onPick={(title) => openExplain(explain.server, Number(title.key))}
            />
            {explain.loading ? (
              <div className="rounded-md border border-border px-3 py-2.5">
                <Spinner size={14} label={t("Checking the title")} />
              </div>
            ) : explain.answer ? (
              <div className="space-y-2 rounded-md border border-border px-3 py-2.5">
                <div className="font-medium">
                  {explain.answer.title || t("Title {key}", { key: explain.answer.key })}
                </div>
                <p className="text-fg-muted">{stepAsideCauseText(explain.answer)}</p>
                {explain.answer.kind === "show" && explain.answer.cause === "missing_episodes" ? (
                  <div className="space-y-1">
                    <p className="text-fg-muted">
                      {t("{held} of {aired} aired episodes are on {server}. Missing:", {
                        held: explain.answer.heldEpisodes,
                        aired: explain.answer.airedRegular,
                        server: explain.answer.servers.join(", ") || t("your server"),
                      })}
                    </p>
                    <p className="text-fg-muted">
                      {[
                        ...explain.answer.missing,
                        ...(explain.answer.missingMore
                          ? [
                              t("and {count} more", {
                                count: explain.answer.missingMore,
                              }),
                            ]
                          : []),
                      ].join(", ")}
                    </p>
                  </div>
                ) : null}
              </div>
            ) : (
              <p className="text-fg-muted">
                {t("Search for a movie or show to see why it is hidden or still showing.")}
              </p>
            )}
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
