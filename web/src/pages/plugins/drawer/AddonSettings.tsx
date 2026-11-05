import { Badge, Button, Dialog, Input, toast } from "@braedonsaunders/appkit-ui";
import { CheckCircle2, ExternalLink, Loader2, Sparkles, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Section } from "../../../components/layout";
import { t } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { invalidateRegistry } from "../../../lib/extend";
import { REDACTED } from "../../../lib/types";
import { SchemaGroups, type FieldDef, type FieldGroup } from "../../settings/SchemaForm";
import type { PluginDetail } from "../types";
import { LiveFields, useLiveForm } from "./liveForm";

interface ConfigField {
  key: string;
  label: string;
  kind: "text" | "number" | "switch" | "list" | "secret" | "select" | "fixed";
  value?: unknown;
  set?: boolean;
  options?: { value: string; label: string }[];
  required?: boolean;
  summary?: string;
  /** The nested setting this field belongs to, e.g. "Languages". */
  group?: string;
}

interface AddonConfig {
  configurable: boolean;
  required: boolean;
  configured: boolean;
  layer: "declared" | "decoded" | "page" | "none";
  fields: ConfigField[];
  host: string;
}

interface ConfigResult {
  name: string;
  added?: string[];
  removed?: string[];
  roles: string[];
  sample?: string;
  problems?: string[];
}

/** Server-read settings (declared or decoded) as Ether fields, one tab per group. */
function serverGroups(cfg: AddonConfig, title: string, description: string): FieldGroup[] {
  const order: string[] = [];
  const by: Record<string, FieldDef[]> = {};
  for (const f of cfg.fields) {
    const g = f.group ?? "";
    if (!(g in by)) {
      by[g] = [];
      order.push(g);
    }
    by[g]!.push(serverDef(f));
  }
  return order.map((g) => ({
    id: g || "plugin",
    title: g || title,
    description: g ? undefined : description,
    fields: by[g]!,
  }));
}

function serverDef(f: ConfigField): FieldDef {
  const base = { key: f.key, label: f.label };
  switch (f.kind) {
    case "switch":
      return { ...base, kind: "bool" };
    case "number":
      return { ...base, kind: "number" };
    case "list":
      return { ...base, kind: "list" };
    case "secret":
      return { ...base, kind: "secret" };
    case "select":
      return { ...base, kind: "select", options: f.options ?? [] };
    case "fixed":
      return { ...base, kind: "readonly" };
    default:
      return { ...base, kind: "text" };
  }
}

function serverValues(cfg: AddonConfig): Record<string, unknown> {
  const v: Record<string, unknown> = {};
  for (const f of cfg.fields)
    v[f.key] =
      f.kind === "secret" ? (f.set ? REDACTED : "") : f.kind === "fixed" ? f.summary : f.value;
  return v;
}

/**
 * A plugin's own settings, inside Ether, as Ether fields. A plugin that
 * declares its settings gets them as declared. Otherwise Ether reads the
 * settings page the plugin publishes and shows that page's real fields
 * (choices, lists, switches) as its own; changes are made on the page,
 * which writes the new link itself. If the page can't be read, settings
 * carried in the link are shown instead. Every change is tried before it
 * is saved.
 */
export function AddonSettings({
  detail,
  onSaved,
  showEmpty = true,
}: {
  detail: PluginDetail;
  onSaved: () => void;
  showEmpty?: boolean;
}) {
  const [cfg, setCfg] = useState<AddonConfig | null>(null);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [touched, setTouched] = useState<Record<string, unknown>>({});
  const [result, setResult] = useState<ConfigResult | null>(null);
  const [busy, setBusy] = useState<"" | "test" | "save">("");
  const [error, setError] = useState("");
  const [page, setPage] = useState(false);
  const [generation, setGeneration] = useState(0);
  const base = `/plugins/${encodeURIComponent(detail.id)}/addon-config`;

  const load = useCallback(async () => {
    try {
      const c = await apiFetch<AddonConfig>(base);
      setError("");
      setCfg(c);
      setValues(serverValues(c));
      setResult(null);
    } catch (e) {
      setError(errorMessage(e));
      setCfg(null);
    }
  }, [base]);
  useEffect(() => {
    void load();
  }, [load]);

  const openPage = () =>
    apiFetch<{ page: string }>(`${base}/page`, { method: "POST", body: "{}" }).then((r) => r.page);
  const wantLive = Boolean(cfg?.configurable && cfg.layer !== "declared" && cfg.layer !== "none");
  const live = useLiveForm(openPage, wantLive ? generation + 1 : 0);

  if (!cfg)
    return error ? (
      <p role="alert" className="text-sm text-danger">
        {error}
      </p>
    ) : null;
  if (!cfg.configurable)
    return showEmpty ? (
      <Section title={t("Settings")}>
        <p className="text-sm text-fg-muted">{t("Nothing to set up. It works as installed.")}</p>
      </Section>
    ) : null;

  const run = async (kind: "test" | "save", body: () => Promise<object | null>) => {
    setBusy(kind);
    setError("");
    try {
      const payload = await body();
      if (!payload) {
        setError(t("{host} didn't give a link for these settings yet.", { host: cfg.host }));
        return;
      }
      if (kind === "test") {
        setResult(
          await apiFetch<ConfigResult>(`${base}/test`, {
            method: "POST",
            body: JSON.stringify(payload),
          }),
        );
      } else {
        await apiFetch(base, { method: "PUT", body: JSON.stringify(payload) });
        invalidateRegistry();
        toast.success(t("Saved {name}'s settings", { name: detail.name }));
        setTouched({});
        await load();
        onSaved();
      }
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy("");
    }
  };

  const fromPage = async () => {
    const link = await live.commit();
    return link ? { link } : null;
  };
  const fromFields = async () => {
    const out: Record<string, unknown> = {};
    for (const f of cfg.fields) {
      const v = values[f.key];
      out[f.key] = f.kind === "secret" && v === REDACTED ? "" : v;
    }
    return { values: out };
  };

  const title = t("Plugin settings");
  const useLive = live.status === "ready";
  const native = cfg.layer === "declared" || cfg.layer === "decoded";
  const actions = (body: () => Promise<object | null>) => (
    <div className="flex w-full flex-wrap items-center gap-2">
      <Button variant="ghost" size="sm" onClick={() => setPage(true)}>
        <ExternalLink size={13} /> {t("Open {host}'s own page", { host: cfg.host })}
      </Button>
      <div className="ml-auto flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={busy !== ""}
          onClick={() => void run("test", body)}
        >
          {busy === "test" ? <Loader2 size={13} className="animate-spin" /> : null}
          {t("Try it")}
        </Button>
        <Button size="sm" disabled={busy !== ""} onClick={() => void run("save", body)}>
          {busy === "save" ? t("Saving…") : t("Save")}
        </Button>
      </div>
    </div>
  );

  let body: ReactNode;
  if (useLive) {
    body = (
      <LiveFields
        form={live}
        title={title}
        description={
          live.partial
            ? t(
                "Read from {host}'s own settings page. A few of its settings are only on that page.",
                { host: cfg.host },
              )
            : t("Read from {host}'s own settings page. Changes are tried before they're saved.", {
                host: cfg.host,
              })
        }
        touched={touched}
        onTouched={(next) => {
          setTouched(next);
          setResult(null);
        }}
        footer={actions(fromPage)}
      />
    );
  } else if (live.status === "loading") {
    body = (
      <Section title={title}>
        <p className="flex items-center gap-2 text-sm text-fg-muted" role="status">
          <Loader2 size={14} className="animate-spin" aria-hidden />
          {t("Reading {host}'s settings…", { host: cfg.host })}
        </p>
      </Section>
    );
  } else if (native) {
    body = (
      <SchemaGroups
        groups={serverGroups(
          cfg,
          title,
          t("Read from {host}. Changes are tried before they're saved.", { host: cfg.host }),
        )}
        value={values}
        errors={{}}
        prefix="plugin"
        onChange={(next) => {
          setValues(next);
          setResult(null);
        }}
        footer={actions(fromFields)}
      />
    );
  } else {
    body = (
      <Section title={title}>
        <p className="text-sm text-fg-muted">
          {t("{host} has its own settings page. Set it up here; Ether picks up the result.", {
            host: cfg.host,
          })}
        </p>
        <Button className="mt-3" onClick={() => setPage(true)}>
          <ExternalLink size={14} /> {t("Open {host} settings", { host: cfg.host })}
        </Button>
      </Section>
    );
  }

  return (
    <div className="space-y-3">
      {cfg.required && !cfg.configured ? (
        <p
          role="status"
          className="flex items-center gap-2 rounded-lg bg-warning-subtle px-3 py-2 text-sm"
        >
          <TriangleAlert size={15} className="shrink-0 text-warning" aria-hidden />
          {t("{name} needs setting up before Ether can use it.", { name: detail.name })}
        </p>
      ) : null}
      {body}
      {result ? <ResultCard result={result} /> : null}
      {error ? (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : null}
      {live.frame}
      <AddonSettingsPage
        open={page}
        name={detail.name}
        host={cfg.host}
        openPage={openPage}
        tryLink={(link) =>
          apiFetch<ConfigResult>(`${base}/test`, { method: "POST", body: JSON.stringify({ link }) })
        }
        onUse={async (link) => {
          await apiFetch(base, { method: "PUT", body: JSON.stringify({ link }) });
          invalidateRegistry();
          toast.success(t("Saved {name}'s settings", { name: detail.name }));
          setPage(false);
          setTouched({});
          await load();
          setGeneration((g) => g + 1);
          onSaved();
        }}
        onClose={() => setPage(false)}
      />
    </div>
  );
}

function ResultCard({ result }: { result: ConfigResult }) {
  const ok = !result.problems?.length;
  return (
    <div className="space-y-2 rounded-xl border border-border bg-surface px-4 py-3" role="status">
      <p className="flex items-center gap-2 text-sm font-medium text-fg">
        {ok ? (
          <CheckCircle2 size={16} className="text-success" aria-hidden />
        ) : (
          <TriangleAlert size={16} className="text-warning" aria-hidden />
        )}
        {result.sample || (ok ? t("The plugin answers with these settings.") : result.problems![0])}
      </p>
      {result.added?.length || result.removed?.length ? (
        <div className="flex flex-wrap gap-1.5">
          {(result.added ?? []).map((c) => (
            <Badge key={`+${c}`} variant="success">
              + {c}
            </Badge>
          ))}
          {(result.removed ?? []).map((c) => (
            <Badge key={`-${c}`} variant="secondary">
              − {c}
            </Badge>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/**
 * The add-on's own settings page, inside Ether. It runs sandboxed through
 * Ether's relay; when the page produces its install link (Install button,
 * copy, or shown on the page) Ether tries it and offers to save it.
 */
export function AddonSettingsPage({
  open,
  name,
  host,
  openPage,
  tryLink,
  onUse,
  onClose,
  useLabel,
}: {
  open: boolean;
  name: string;
  host: string;
  openPage: () => Promise<string>;
  tryLink?: (link: string) => Promise<ConfigResult>;
  onUse: (link: string) => Promise<void>;
  onClose: () => void;
  useLabel?: string;
}) {
  const [src, setSrc] = useState("");
  const [link, setLink] = useState("");
  const [candidate, setCandidate] = useState("");
  const [pasted, setPasted] = useState("");
  const [result, setResult] = useState<ConfigResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const frame = useRef<HTMLIFrameElement>(null);

  useEffect(() => {
    if (!open) return;
    setSrc("");
    setLink("");
    setCandidate("");
    setPasted("");
    setResult(null);
    setError("");
    void openPage()
      .then(setSrc)
      .catch((e) => setError(errorMessage(e)));
    // openPage is a fresh closure per render; the page opens once per open.
  }, [open]);

  const take = useCallback(
    async (url: string) => {
      setLink(url);
      setResult(null);
      setError("");
      if (!tryLink) return;
      setBusy(true);
      try {
        setResult(await tryLink(url));
      } catch (e) {
        setError(errorMessage(e));
      } finally {
        setBusy(false);
      }
    },
    [tryLink],
  );

  useEffect(() => {
    if (!open) return;
    const onMessage = (e: MessageEvent) => {
      if (e.source !== frame.current?.contentWindow) return;
      const d = e.data as { source?: string; type?: string; url?: string };
      if (d?.source !== "ether-addon-relay" || typeof d.url !== "string") return;
      if (d.type === "link") void take(d.url);
      else if (d.type === "candidate") setCandidate(d.url);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [open, take]);

  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="xl"
      fullHeight
      title={t("{name} settings", { name })}
      description={t(
        "This is {host}'s own page. When it gives you an install link, Ether picks it up.",
        {
          host,
        },
      )}
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          {link ? (
            <span className="flex min-w-0 flex-1 items-center gap-2 text-sm">
              {busy ? (
                <Loader2 size={14} className="animate-spin text-fg-muted" aria-hidden />
              ) : result?.problems?.length || error ? (
                <TriangleAlert size={14} className="text-warning" aria-hidden />
              ) : (
                <Sparkles size={14} className="text-primary" aria-hidden />
              )}
              <span className="truncate text-fg">
                {busy
                  ? t("Trying the new settings…")
                  : error ||
                    result?.sample ||
                    result?.problems?.[0] ||
                    t("Settings ready from {host}", { host })}
              </span>
            </span>
          ) : candidate ? (
            <span className="flex min-w-0 flex-1 items-center gap-2 text-sm text-fg-muted">
              {t("The page shows an install link.")}
              <Button size="sm" variant="outline" onClick={() => void take(candidate)}>
                {t("Use it")}
              </Button>
            </span>
          ) : (
            <form
              className="flex min-w-0 flex-1 items-center gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                if (pasted.trim()) void take(pasted.trim());
              }}
            >
              <Input
                aria-label={t("Or paste the install link")}
                placeholder={t("Or paste the install link it gives you")}
                value={pasted}
                onChange={(e) => setPasted(e.currentTarget.value)}
              />
              <Button type="submit" size="sm" variant="outline" disabled={!pasted.trim()}>
                {t("Use")}
              </Button>
            </form>
          )}
          <Button variant="ghost" onClick={onClose}>
            {t("Cancel")}
          </Button>
          <Button
            disabled={!link || busy || Boolean(error)}
            onClick={() => {
              setBusy(true);
              void onUse(link)
                .catch((e) => setError(errorMessage(e)))
                .finally(() => setBusy(false));
            }}
          >
            {useLabel ?? t("Save settings")}
          </Button>
        </div>
      }
    >
      {src ? (
        <iframe
          ref={frame}
          title={t("{host} settings page", { host })}
          src={src}
          sandbox="allow-scripts allow-forms allow-popups allow-modals"
          referrerPolicy="no-referrer"
          className="h-full min-h-[60vh] w-full flex-1 border-0 bg-white"
        />
      ) : (
        <div className="grid flex-1 place-items-center p-8 text-sm text-fg-muted">
          {error || (
            <span className="flex items-center gap-2">
              <Loader2 size={16} className="animate-spin" aria-hidden />{" "}
              {t("Opening {host}…", { host })}
            </span>
          )}
        </div>
      )}
    </Dialog>
  );
}
