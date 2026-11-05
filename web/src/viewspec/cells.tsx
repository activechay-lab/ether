/**
 * Cell renderers: the closed set of ways a table cell may present a value.
 * Values arrive already formatted by the plugin's loader (money, dates and
 * numbers are display strings); cells add only alignment, tone and chrome.
 * Plugin documents carry no class names, so every treatment here is Ether's.
 */
import { Badge, cn } from "@braedonsaunders/appkit-ui";
import type { CellSpec, LeafCell, Tone } from "@braedonsaunders/appkit-viewspec";
import type { ReactNode } from "react";
import { resolveIn, resolveProps, textIn, valueIn } from "./context";
import { EtherWidget } from "./widgets";

export function toneClass(tone: Tone | undefined): string {
  switch (tone) {
    case "negative":
      return "text-danger";
    case "positive":
      return "text-success";
    case "warning":
      return "text-warning";
    case "muted":
      return "text-fg-muted";
    case "strong":
      return "font-semibold text-fg";
    default:
      return "text-fg";
  }
}

const BADGE_VARIANTS = [
  "default",
  "secondary",
  "outline",
  "destructive",
  "warning",
  "success",
] as const;

type BadgeVariant = (typeof BADGE_VARIANTS)[number];

function badgeVariant(raw: string | undefined): BadgeVariant {
  return (BADGE_VARIANTS as readonly string[]).includes(raw ?? "")
    ? (raw as BadgeVariant)
    : "secondary";
}

/**
 * A link target a plugin may send the owner to: a console-relative path or
 * an https page. Anything else (javascript:, data:, protocol-relative)
 * renders as plain text.
 */
export function safeHref(raw: string): string | null {
  if (!raw || /[\\\u0000-\u001f\u007f]/.test(raw)) return null;
  if (raw.startsWith("/") && !raw.startsWith("//")) return raw;
  if (!raw.startsWith("https://")) return null;
  try {
    const url = new URL(raw);
    return url.protocol === "https:" && url.hostname && !url.username && !url.password ? raw : null;
  } catch {
    return null;
  }
}

function CellLink({ href, children }: { href: string; children: ReactNode }) {
  const external = href.startsWith("https://");
  return (
    <a
      href={href}
      {...(external ? { target: "_blank", rel: "noreferrer" } : {})}
      className="font-medium text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 rounded-sm"
    >
      {children}
    </a>
  );
}

function LeafCellView({ cell, scope, root }: { cell: LeafCell; scope: unknown; root: unknown }) {
  switch (cell.kind) {
    case "text": {
      const raw = resolveIn(scope, root, cell.field);
      const tone = valueIn(cell.tone, scope, root);
      const prefix = cell.prefix ? textIn(cell.prefix.field, scope, root) : "";
      const suffix = cell.suffix ? textIn(cell.suffix.field, scope, root) : "";
      if (raw === undefined || raw === null || raw === "") {
        const fallback = textIn(cell.fallback, scope, root);
        if (!fallback && !prefix && !suffix) {
          return <span className="text-fg-subtle">—</span>;
        }
        return (
          <span>
            {prefix ? <span className="mr-1.5 text-fg-muted">{prefix}</span> : null}
            <span className="italic text-fg-subtle">{fallback}</span>
            {suffix ? <span className="ml-1.5 text-fg-muted">{suffix}</span> : null}
          </span>
        );
      }
      return (
        <span className={cn(toneClass(tone), cell.numeric && "tabular-nums")}>
          {prefix ? <span className="mr-1.5 text-fg-muted">{prefix}</span> : null}
          {String(raw)}
          {suffix ? <span className="ml-1.5 text-fg-muted">{suffix}</span> : null}
        </span>
      );
    }
    case "money":
    case "number": {
      const raw = resolveIn(scope, root, cell.field);
      const tone = valueIn(cell.tone, scope, root);
      return (
        <span className={cn("tabular-nums", toneClass(tone))}>
          {raw === undefined || raw === null ? "—" : String(raw)}
        </span>
      );
    }
    case "date": {
      const raw = resolveIn(scope, root, cell.field);
      return (
        <span className="text-fg">
          {raw === undefined || raw === null || raw === "" ? "—" : String(raw)}
        </span>
      );
    }
    case "badge": {
      const raw = resolveIn(scope, root, cell.field);
      if (raw === undefined || raw === null || raw === "") {
        return <span className="text-fg-subtle">—</span>;
      }
      const variant = valueIn(cell.variant, scope, root);
      return <Badge variant={badgeVariant(variant)}>{String(raw)}</Badge>;
    }
    case "link": {
      const label = textIn(cell.field, scope, root);
      const href = safeHref(textIn(cell.href, scope, root));
      if (!href) return <span className="text-fg">{label}</span>;
      return <CellLink href={href}>{label}</CellLink>;
    }
    case "record-link": {
      // The console has no deep route per record type for plugins to aim at,
      // so a record reference renders as its label. The id stays in the
      // document for the day a host route exists.
      return <span className="font-medium text-fg">{textIn(cell.field, scope, root)}</span>;
    }
  }
}

export function CellView({ cell, scope, root }: { cell: CellSpec; scope: unknown; root: unknown }) {
  switch (cell.kind) {
    case "drill":
    case "txn": {
      // Drill and transaction wrappers link into host pages the accounting
      // vocabulary assumed. Ether honors an explicit href on the target the
      // loader built, and otherwise shows the value itself.
      const target = resolveIn(scope, root, cell.target);
      const href =
        typeof target === "object" && target !== null && "href" in target
          ? safeHref(String((target as { href?: unknown }).href ?? ""))
          : null;
      const inner = <LeafCellView cell={cell.inner} scope={scope} root={root} />;
      return href ? <CellLink href={href}>{inner}</CellLink> : inner;
    }
    case "widget":
      return <EtherWidget name={cell.widget} props={resolveProps(cell.props, scope, root)} />;
    default:
      return <LeafCellView cell={cell} scope={scope} root={root} />;
  }
}
