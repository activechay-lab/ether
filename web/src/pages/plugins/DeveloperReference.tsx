import { useEffect, useMemo, useRef, useState, isValidElement, type ReactElement } from "react";
import { Link, useParams, useSearchParams, useLocation } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Button, buttonVariants } from "@braedonsaunders/appkit-ui";
import { DomainPage } from "../../components/layout";
import { t } from "../../i18n";
import reference from "./developer-reference.json";
import guides from "./reference-guides.json";
import "./reference.css";

const corpus = reference.map((section) => ({
  ...section,
  search: `${section.path}\n${section.code}`.toLowerCase(),
}));
function matches(path: string, _code: string, patterns: string[]) {
  return patterns.some((pattern) =>
    pattern.startsWith("@host:")
      ? (new RegExp(pattern.slice(6), "i").test(path.split("#")[1] ?? "") &&
          /host|contract|sdk/.test(path)) ||
        (pattern.startsWith("@host:") && path.includes("sdk/typescript/src/host.ts"))
      : path.startsWith(pattern),
  );
}
function Copy({ value }: { value: string }) {
  const [state, setState] = useState<"idle" | "done" | "error">("idle");
  return (
    <Button
      size="sm"
      variant="outline"
      onClick={() => {
        if (!navigator.clipboard) {
          setState("error");
          return;
        }
        void navigator.clipboard.writeText(value).then(
          () => setState("done"),
          () => setState("error"),
        );
      }}
    >
      {state === "done" ? t("Copied") : state === "error" ? t("Copy failed") : t("Copy")}
    </Button>
  );
}
function Markdown({ text }: { text: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        a: ({ href, children }) =>
          href?.startsWith("/plugins/reference/") ? (
            <Link to={href}>{children}</Link>
          ) : (
            <a
              href={
                href?.startsWith("http") || href?.startsWith("#")
                  ? href
                  : `https://github.com/braedonsaunders/ether/tree/main/${href?.replace(/^(\.\.\/)+/, "") ?? ""}`
              }
            >
              {children}
            </a>
          ),
        pre: ({ children }) => {
          const value = isValidElement(children)
            ? String((children as ReactElement<{ children?: unknown }>).props.children ?? "")
            : String(children);
          return (
            <div>
              <pre>{children}</pre>
              <Copy value={value} />
            </div>
          );
        },
      }}
    >
      {text}
    </ReactMarkdown>
  );
}
export default function DeveloperReference() {
  const location = useLocation();
  const { topic = "getting-started" } = useParams();
  const [params, setParams] = useSearchParams();
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  useEffect(() => {
    setQuery(params.get("q") ?? "");
  }, [location.pathname, location.search]);
  useEffect(() => {
    if (query === (params.get("q") ?? "")) return;
    const timer = window.setTimeout(
      () => setParams(query ? { q: query } : {}, { replace: true }),
      150,
    );
    return () => window.clearTimeout(timer);
  }, [query, location.pathname, params, setParams]);
  const input = useRef<HTMLInputElement>(null);
  const article = useRef<HTMLElement>(null);
  const guide = guides.find((g) => g.id === topic);
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const results = useMemo(
    () =>
      terms.length
        ? [
            ...guides
              .filter((g) =>
                terms.every((term) => `${g.title} ${g.body}`.toLowerCase().includes(term)),
              )
              .map((g) => ({ path: g.title, code: g.body, topic: g.id })),
            ...corpus
              .filter((s) => terms.every((term) => s.search.includes(term)))
              .map((s) => ({
                ...s,
                topic: guides.find((g) => matches(s.path, s.code, g.patterns))?.id ?? "manifest",
              })),
          ]
        : [],
    [query],
  ); // Local immutable corpus; no server or indexer calls.
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (
        ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") ||
        (e.key === "/" &&
          !(
            e.target instanceof HTMLInputElement ||
            e.target instanceof HTMLTextAreaElement ||
            (e.target instanceof HTMLElement && e.target.isContentEditable)
          ))
      ) {
        e.preventDefault();
        e.stopImmediatePropagation();
        input.current?.focus();
        input.current?.select();
      }
      if (e.key === "Escape" && document.activeElement === input.current) {
        setQuery("");
        setParams({}, { replace: true });
        input.current?.blur();
      }
    };
    window.addEventListener("keydown", key, true);
    return () => window.removeEventListener("keydown", key, true);
  }, [setParams]);
  useEffect(() => {
    if (!location.hash) return;
    let id: string;
    try {
      id = decodeURIComponent(location.hash.slice(1));
    } catch {
      return;
    }
    const target = document.getElementById(id);
    if (target instanceof HTMLDetailsElement) target.open = true;
    target?.scrollIntoView({ block: "start" });
  }, [topic, location.hash, query]);
  const sections = guide ? corpus.filter((s) => matches(s.path, s.code, guide.patterns)) : [];
  return (
    <DomainPage
      title={t("Developer reference")}
      base="/plugins"
      actions={
        <Link
          className={buttonVariants({ variant: "outline", size: "sm" })}
          to="/plugins/installed"
        >
          {t("Plugins")}
        </Link>
      }
    >
      <div className="space-y-2">
        <label className="block text-sm font-medium" htmlFor="reference-search">
          {t("Search API reference")}
        </label>
        <input
          ref={input}
          id="reference-search"
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("Search")}
          className="w-full rounded-lg border border-border bg-surface px-4 py-3 text-sm"
        />
        <p className="text-xs text-fg-muted">
          {t("Keyboard shortcut")}: <kbd>⌘ / Ctrl K</kbd>
        </p>
      </div>
      <div className="grid items-start gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
        <nav
          aria-label={t("Topics")}
          className="max-h-[65vh] overflow-auto rounded-xl border border-border p-3 lg:sticky lg:top-4"
        >
          {[...new Set(guides.map((g) => g.group))].map((group) => (
            <div key={group} className="mb-5">
              <p className="mb-2 px-3 text-xs font-semibold text-fg-muted">{group}</p>
              {guides
                .filter((g) => g.group === group)
                .map((g) => (
                  <Link
                    key={g.id}
                    to={`/plugins/reference/${g.id}`}
                    aria-current={topic === g.id ? "page" : undefined}
                    className={`block rounded-lg px-3 py-2 text-sm hover:bg-surface ${topic === g.id ? "bg-surface font-semibold text-accent" : "text-fg-muted"}`}
                  >
                    {g.title}
                  </Link>
                ))}
            </div>
          ))}
        </nav>
        <article ref={article} className="dev-reference min-w-0 space-y-6" lang="en">
          {terms.length ? (
            <>
              <h2 className="text-xl font-semibold">
                {t("Search results")} <span className="text-fg-muted">({results.length})</span>
              </h2>
              {results.length === 0 ? <p>{t("No results")}</p> : null}
              {results.slice(0, 100).map((s) => {
                const at = Math.max(0, s.code.toLowerCase().indexOf(terms[0] ?? ""));
                return (
                  <section key={s.path} className="rounded-xl border border-border p-4">
                    <Link
                      className="break-all font-medium text-accent"
                      to={`/plugins/reference/${s.topic}${s.path.includes("/") ? `#${encodeURIComponent(s.path)}` : ""}`}
                    >
                      {s.path}
                    </Link>
                    <pre className="mt-3 whitespace-pre-wrap text-xs text-fg-muted">
                      {s.code.slice(Math.max(0, at - 100), at + 500)}
                    </pre>
                  </section>
                );
              })}
              {results.length > 100 ? <p>{t("Refine your search")}</p> : null}
            </>
          ) : guide ? (
            <>
              <h1 className="text-2xl font-semibold">{guide.title}</h1>
              <Markdown text={guide.body} />
              {sections.map((s) =>
                s.path.endsWith(".md") && guide.id !== "all-contract" ? (
                  <section key={s.path} id={s.path} className="scroll-mt-6">
                    <h2 className="break-all text-base font-semibold">{s.path}</h2>
                    <Markdown text={s.code} />
                  </section>
                ) : (
                  <details
                    key={s.path}
                    id={s.path}
                    className="scroll-mt-6 rounded-xl border border-border p-4"
                  >
                    <summary className="cursor-pointer break-all font-mono text-xs">
                      {s.path}
                    </summary>
                    <div className="mt-3">
                      <Copy value={s.code} />
                      <pre className="mt-3">
                        <code>{s.code}</code>
                      </pre>
                    </div>
                  </details>
                ),
              )}
            </>
          ) : (
            <>
              <h1>{t("No results")}</h1>
              <Link to="/plugins/reference/getting-started">{t("Reference")}</Link>
            </>
          )}
        </article>
      </div>
    </DomainPage>
  );
}
