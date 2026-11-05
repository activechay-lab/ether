import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { t } from "../../../i18n";
import { errorMessage } from "../../../lib/api";
import { REDACTED } from "../../../lib/types";
import { SchemaGroups, type FieldDef, type FieldGroup } from "../../settings/SchemaForm";

/** A setting read from a plugin's own settings page (see formJS in Go). */
interface LiveField {
  id: string;
  kind: "text" | "number" | "secret" | "bool" | "select" | "multiSelect";
  label: string;
  value?: unknown;
  set?: boolean;
  placeholder?: string;
  options?: { value: string; label: string }[];
  min?: number;
  max?: number;
  step?: number;
}

interface LiveForm {
  /** The hidden page; render it anywhere. */
  frame: ReactNode;
  status: "off" | "loading" | "ready" | "failed";
  fields: LiveField[];
  /** The page has settings Ether can't show (tabs, list editors). */
  partial: boolean;
  /** The install link the page shows now, if any. */
  link: string;
  error: string;
  set: (id: string, value: unknown) => void;
  /** The page's install link for the settings as they are now. */
  commit: () => Promise<string>;
}

const READ_TIMEOUT_MS = 20_000;
const EMPTY_GRACE_MS = 6_000;

/**
 * Runs a plugin's own settings page out of sight and reads its form, so
 * Ether can show those settings as its own fields. Every change is made on
 * the page's real control and the page writes the install link itself, so
 * nothing here knows any one plugin.
 */
export function useLiveForm(
  openPage: () => Promise<string>,
  /** 0 is off; any other value opens the page, and a new value reopens it. */
  enabled: number,
): LiveForm {
  const [src, setSrc] = useState("");
  const [status, setStatus] = useState<LiveForm["status"]>("off");
  const [fields, setFields] = useState<LiveField[]>([]);
  const [partial, setPartial] = useState(false);
  const [link, setLink] = useState("");
  const [error, setError] = useState("");
  const frame = useRef<HTMLIFrameElement>(null);
  // Which link answers which change: a link counts once it arrived after
  // the latest change the page was asked to make.
  const changeAt = useRef(0);
  const linkAt = useRef(0);
  const linkRef = useRef("");
  const waiters = useRef<(() => void)[]>([]);
  // A page that has loaded but shows nothing Ether can read gives up soon,
  // so its own page is offered without the full wait.
  const emptyTimer = useRef(0);

  useEffect(() => {
    if (!enabled) {
      setStatus("off");
      return;
    }
    let live = true;
    setStatus("loading");
    setFields([]);
    setLink("");
    linkRef.current = "";
    openPage()
      .then((s) => live && setSrc(s))
      .catch((e) => {
        if (!live) return;
        setError(errorMessage(e));
        setStatus("failed");
      });
    const timer = window.setTimeout(
      () => live && setStatus((s) => (s === "loading" ? "failed" : s)),
      READ_TIMEOUT_MS,
    );
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
    // openPage is a fresh closure per render; the page opens once per enable.
  }, [enabled]);

  useEffect(() => {
    if (!enabled) return;
    const onMessage = (e: MessageEvent) => {
      if (!frame.current || e.source !== frame.current.contentWindow) return;
      const d = e.data as {
        source?: string;
        type?: string;
        url?: string;
        fields?: LiveField[];
        partial?: boolean;
      };
      if (d?.source !== "ether-addon-relay") return;
      if (d.type === "form" && Array.isArray(d.fields)) {
        setFields(d.fields);
        setPartial(Boolean(d.partial));
        window.clearTimeout(emptyTimer.current);
        if (d.fields.length) setStatus("ready");
        else
          emptyTimer.current = window.setTimeout(
            () => setStatus((s) => (s === "loading" ? "failed" : s)),
            EMPTY_GRACE_MS,
          );
      } else if ((d.type === "link" || d.type === "candidate") && typeof d.url === "string") {
        linkRef.current = d.url;
        linkAt.current = performance.now();
        setLink(d.url);
        waiters.current.splice(0).forEach((w) => w());
      }
    };
    window.addEventListener("message", onMessage);
    return () => {
      window.removeEventListener("message", onMessage);
      window.clearTimeout(emptyTimer.current);
    };
  }, [enabled]);

  const post = useCallback((msg: object) => {
    frame.current?.contentWindow?.postMessage({ source: "ether", ...msg }, "*");
  }, []);

  const set = useCallback(
    (id: string, value: unknown) => {
      changeAt.current = performance.now();
      post({ type: "set", id, value });
    },
    [post],
  );

  const waitForLink = (ms: number) =>
    new Promise<void>((resolve) => {
      const timer = window.setTimeout(resolve, ms);
      waiters.current.push(() => {
        window.clearTimeout(timer);
        resolve();
      });
    });

  const commit = useCallback(async () => {
    const fresh = () => linkRef.current && linkAt.current >= changeAt.current;
    // Pages that rewrite their link as you go answer on their own.
    for (let i = 0; i < 4 && !fresh(); i++) await waitForLink(400);
    if (fresh()) return linkRef.current;
    // Others make it when asked: press their own copy or install control.
    post({ type: "commit" });
    for (let i = 0; i < 8 && !fresh(); i++) await waitForLink(500);
    return linkRef.current;
  }, [post]);

  const node = src ? (
    <iframe
      ref={frame}
      src={src}
      title={t("Plugin settings page")}
      aria-hidden
      tabIndex={-1}
      sandbox="allow-scripts allow-forms allow-popups allow-modals"
      referrerPolicy="no-referrer"
      // In the viewport but invisible and behind everything: browsers slow
      // down frames that are off-screen, and the page must keep up.
      style={{
        position: "fixed",
        left: 0,
        top: 0,
        zIndex: -1,
        width: 1200,
        height: 900,
        opacity: 0,
        pointerEvents: "none",
        border: 0,
      }}
    />
  ) : null;

  return { frame: node, status, fields, partial, link, error, set, commit };
}

function unique(options: LiveField["options"]) {
  const seen = new Set<string>();
  return (options ?? []).filter((o) => !seen.has(o.value) && seen.add(o.value));
}

/** A live field as one of Ether's own settings fields. */
function fieldDef(f: LiveField): FieldDef {
  const base = { key: f.id, label: f.label };
  switch (f.kind) {
    case "bool":
      return { ...base, kind: "bool" };
    case "select":
      return { ...base, kind: "select", options: unique(f.options) };
    case "multiSelect":
      return { ...base, kind: "multiSelect", options: unique(f.options) };
    case "secret":
      return { ...base, kind: "secret", placeholder: f.placeholder };
    case "number":
      // An empty number keeps its hint ("All results") instead of reading 0.
      return f.value === null || f.value === undefined
        ? { ...base, kind: "text", placeholder: f.placeholder }
        : { ...base, kind: "number", min: f.min, max: f.max, step: f.step };
    default:
      return { ...base, kind: "text", placeholder: f.placeholder };
  }
}

function pageValue(f: LiveField): unknown {
  if (f.kind === "secret") return f.set ? REDACTED : "";
  return f.value;
}

/**
 * The page's settings as Ether fields. What a person changes here wins over
 * what the page echoes back while it catches up.
 */
export function LiveFields({
  form,
  title,
  description,
  footer,
  touched,
  onTouched,
}: {
  form: LiveForm;
  title: string;
  description?: string;
  footer?: ReactNode;
  touched: Record<string, unknown>;
  onTouched: (next: Record<string, unknown>) => void;
}) {
  const groups: FieldGroup[] = useMemo(
    () => [{ id: "plugin", title, description, fields: form.fields.map(fieldDef) }],
    [form.fields, title, description],
  );
  const value = useMemo(() => {
    const v: Record<string, unknown> = {};
    for (const f of form.fields) v[f.id] = f.id in touched ? touched[f.id] : pageValue(f);
    return v;
  }, [form.fields, touched]);
  return (
    <SchemaGroups
      groups={groups}
      value={value}
      errors={{}}
      prefix="plugin"
      footer={footer}
      onChange={(next) => {
        const changed: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(next)) {
          if (JSON.stringify(v) === JSON.stringify(value[k])) continue;
          changed[k] = v;
          if (v !== REDACTED) form.set(k, v);
        }
        if (Object.keys(changed).length) onTouched({ ...touched, ...changed });
      }}
    />
  );
}
