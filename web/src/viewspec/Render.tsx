/**
 * The ViewSpec renderer for plugin documents. Every v1 block and cell kind
 * renders with Ether and AppKit components, reading only resolved data: the
 * plugin's loader computed, this file binds. No plugin class names, markup
 * or code ever reach the DOM; validation (validate.ts) already refused any
 * document that tried.
 */
import {
  Button,
  EmptyState,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  cn,
} from "@braedonsaunders/appkit-ui";
import type { Block, PageSpec, TableBlock, WidgetRef } from "@braedonsaunders/appkit-viewspec";
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Clock,
  Film,
  Play,
  Search,
  Star,
  Tv,
  Users,
} from "lucide-react";
import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { t } from "../i18n";
import { Section } from "../components/layout";
import { CellView, safeHref, toneClass } from "./cells";
import {
  numberIn,
  resolveIn,
  resolveProps,
  rowsIn,
  textIn,
  usePluginView,
  valueIn,
  visible,
} from "./context";
import { EtherFrame, EtherWidget } from "./widgets";

/** Plugin tables show at most this many rows per page (SPEC §5.6). */
const MAX_TABLE_ROWS = 100;

function WidgetRefView({
  refSpec,
  scope,
  root,
}: {
  refSpec: WidgetRef;
  scope: unknown;
  root: unknown;
}) {
  if (!visible(refSpec.when, scope, root)) return null;
  return <EtherWidget name={refSpec.widget} props={resolveProps(refSpec.props, scope, root)} />;
}

function FilterBarView({
  block,
  scope,
  root,
}: {
  block: Extract<Block, { kind: "filter-bar" }>;
  scope: unknown;
  root: unknown;
}) {
  const view = usePluginView();
  const [query, setQuery] = useState(view.params.q ?? "");
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const onQuery = (value: string) => {
    setQuery(value);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      view.setParams({ q: value.trim() || undefined, page: undefined });
    }, 300);
  };
  return (
    <div className="flex flex-wrap items-center gap-2">
      {block.leading ? (
        <div className="flex flex-wrap items-center gap-1" role="group">
          {block.leading.links.map((link, i) => {
            const active = Boolean(resolveIn(scope, root, link.activeWhen));
            const href = textIn(link.href, scope, root);
            return (
              <Button
                key={i}
                type="button"
                size="sm"
                variant={active ? "default" : "outline"}
                aria-pressed={active}
                onClick={() => {
                  if (href.startsWith("?")) {
                    const patch: Record<string, string | undefined> = {};
                    for (const [k, v] of new URLSearchParams(href.slice(1))) {
                      patch[k] = v;
                    }
                    view.setParams(patch);
                  }
                }}
              >
                {textIn(link.label, scope, root)}
              </Button>
            );
          })}
          {block.leading.divider ? (
            <span className="mx-1 hidden h-5 w-px bg-border sm:block" aria-hidden />
          ) : null}
        </div>
      ) : null}
      {block.controls.search ? (
        <div className="relative min-w-48 flex-1 sm:max-w-xs">
          <Search
            size={14}
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle"
            aria-hidden
          />
          <Input
            className="pl-8"
            value={query}
            onChange={(e) => onQuery(e.currentTarget.value)}
            placeholder={textIn(block.searchPlaceholder, scope, root) || t("Search…")}
            aria-label={t("Search")}
          />
        </div>
      ) : null}
      {block.actions?.length ? (
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {block.actions.map((action, i) => (
            <WidgetRefView key={i} refSpec={action} scope={scope} root={root} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function alignClass(align: "left" | "right" | "center" | undefined): string {
  return align === "right" ? "text-right" : align === "center" ? "text-center" : "text-left";
}

function TableView({ block, scope, root }: { block: TableBlock; scope: unknown; root: unknown }) {
  const view = usePluginView();
  const allRows = rowsIn(block.rows, scope, root);
  const rows = allRows.slice(0, MAX_TABLE_ROWS);
  if (allRows.length === 0 && block.empty && !block.emptyRow) {
    return (
      <EmptyState
        title={textIn(block.empty.title, scope, root)}
        description={textIn(block.empty.description, scope, root) || undefined}
      />
    );
  }
  const sorting = block.sorting;
  const activeSort = sorting ? textIn(sorting.sort, scope, root) : "";
  const activeDir = sorting ? textIn(sorting.dir, scope, root) : "";
  const toggleSort = (key: string) => {
    if (!sorting) return;
    const dir = activeSort === key && activeDir !== "desc" ? "desc" : "asc";
    view.setParams({
      [sorting.sortParamKey ?? "sort"]: key,
      [sorting.dirParamKey ?? "dir"]: dir,
      [sorting.pageParamKey ?? "page"]: undefined,
    });
  };
  const spanRow = (row: NonNullable<TableBlock["leading"]>[number], key: string) => (
    <TableRow key={key} noAnimate className="bg-bg-subtle font-medium">
      <TableCell colSpan={row.labelColSpan}>{textIn(row.label, scope, root)}</TableCell>
      {row.cells.map((entry, i) => (
        <TableCell key={i} className={alignClass(entry.align)}>
          <CellView cell={entry.cell} scope={scope} root={root} />
        </TableCell>
      ))}
    </TableRow>
  );
  return (
    <div className="space-y-2">
      <Table>
        <TableHeader>
          <TableRow noAnimate>
            {block.columns.map((column, i) => {
              const label = textIn(column.header, scope, root);
              const sortable = Boolean(sorting && column.sort);
              const active = sortable && activeSort === column.sort;
              return (
                <TableHead key={i} className={alignClass(column.align)}>
                  {sortable ? (
                    <button
                      type="button"
                      onClick={() => toggleSort(column.sort!)}
                      aria-label={t("Sort by {column}", { column: label })}
                      className="inline-flex items-center gap-1 font-medium uppercase tracking-wide hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 rounded-sm"
                    >
                      {column.srOnlyHeader ? <span className="sr-only">{label}</span> : label}
                      {active ? (
                        activeDir === "desc" ? (
                          <ArrowDown size={12} aria-hidden />
                        ) : (
                          <ArrowUp size={12} aria-hidden />
                        )
                      ) : null}
                    </button>
                  ) : column.srOnlyHeader ? (
                    <span className="sr-only">{label}</span>
                  ) : (
                    label
                  )}
                </TableHead>
              );
            })}
          </TableRow>
        </TableHeader>
        <TableBody>
          {(block.leading ?? []).map((row, i) => spanRow(row, `lead-${i}`))}
          {rows.map((row, i) => {
            const key = resolveIn(row, root, block.rowKey);
            return (
              <TableRow key={key != null && key !== "" ? String(key) : `row-${i}`}>
                {block.columns.map((column, j) => (
                  <TableCell key={j} className={alignClass(column.align)}>
                    <CellView cell={column.cell} scope={row} root={root} />
                  </TableCell>
                ))}
              </TableRow>
            );
          })}
          {allRows.length === 0 ? (
            <TableRow noAnimate>
              <TableCell
                colSpan={block.emptyRow?.colSpan ?? block.columns.length}
                className="py-6 text-center text-fg-muted"
              >
                {block.emptyRow ? textIn(block.emptyRow.text, scope, root) : t("Nothing to show")}
              </TableCell>
            </TableRow>
          ) : null}
          {(block.trailing ?? []).map((row, i) => spanRow(row, `trail-${i}`))}
        </TableBody>
      </Table>
      {allRows.length > MAX_TABLE_ROWS ? (
        <p className="text-xs text-fg-muted">
          {t("Showing the first {shown} of {total} rows", {
            shown: MAX_TABLE_ROWS,
            total: allRows.length,
          })}
        </p>
      ) : null}
    </div>
  );
}

function PaginationView({
  block,
  scope,
  root,
}: {
  block: Extract<Block, { kind: "pagination" }>;
  scope: unknown;
  root: unknown;
}) {
  const view = usePluginView();
  const total = numberIn(block.total, scope, root);
  const page = Math.max(1, Math.trunc(numberIn(block.page, scope, root)) || 1);
  const perPage = Math.max(1, Math.trunc(numberIn(block.perPage, scope, root)) || 1);
  const pages = Math.max(1, Math.ceil(total / perPage));
  const key = block.pageParamKey ?? "page";
  const from = total === 0 ? 0 : (page - 1) * perPage + 1;
  const to = Math.min(total, page * perPage);
  return (
    <div className={cn("flex flex-wrap items-center justify-between gap-2", !block.bare && "mt-3")}>
      <p className="text-sm text-fg-muted">
        {total === 0
          ? t("Nothing to show")
          : t("Showing {from} to {to} of {total}", { from, to, total })}
      </p>
      <div className="flex items-center gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={page <= 1}
          aria-label={t("Previous page")}
          onClick={() => view.setParams({ [key]: String(page - 1) })}
        >
          <ChevronLeft size={14} aria-hidden />
        </Button>
        <span className="text-sm tabular-nums text-fg-muted">
          {t("Page {page} of {pages}", { page, pages })}
        </span>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={page >= pages}
          aria-label={t("Next page")}
          onClick={() => view.setParams({ [key]: String(page + 1) })}
        >
          <ChevronRight size={14} aria-hidden />
        </Button>
      </div>
    </div>
  );
}

const STAT_ICONS: Record<string, typeof Activity> = {
  activity: Activity,
  alert: AlertTriangle,
  check: CheckCircle2,
  clock: Clock,
  film: Film,
  play: Play,
  star: Star,
  tv: Tv,
  users: Users,
};

function BlockView({ block, scope, root }: { block: Block; scope: unknown; root: unknown }) {
  if (!visible("when" in block ? block.when : undefined, scope, root)) return null;
  switch (block.kind) {
    case "page-header": {
      const backHref = block.back ? safeHref(textIn(block.back.href, scope, root)) : null;
      return (
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            {block.back && backHref ? (
              <a
                href={backHref}
                className="mb-1 inline-flex items-center gap-1 text-sm text-fg-muted hover:text-fg"
              >
                <ChevronLeft size={14} aria-hidden />
                {textIn(block.back.label, scope, root)}
              </a>
            ) : null}
            <h2 className="text-lg font-semibold text-fg">{textIn(block.title, scope, root)}</h2>
            {block.description ? (
              <p className="mt-0.5 max-w-2xl text-sm text-fg-muted">
                {textIn(block.description, scope, root)}
              </p>
            ) : null}
          </div>
          {block.actions?.length ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">
              {block.actions.map((action, i) => (
                <WidgetRefView key={i} refSpec={action} scope={scope} root={root} />
              ))}
            </div>
          ) : null}
        </div>
      );
    }
    case "filter-bar":
      return <FilterBarView block={block} scope={scope} root={root} />;
    case "summary-line":
      return (
        <p className="flex flex-wrap items-baseline gap-x-2 text-sm">
          <span className="text-fg-muted">{textIn(block.label, scope, root)}</span>
          <CellView cell={block.value} scope={scope} root={root} />
        </p>
      );
    case "paper": {
      const company = textIn(block.company, scope, root);
      const period = textIn(block.periodPhrase, scope, root);
      const description = [company, period].filter(Boolean).join(" · ");
      return (
        <Section
          title={textIn(block.title, scope, root)}
          description={description || undefined}
          footer={
            block.note ? (
              <p className="w-full text-left text-xs text-fg-muted">
                {textIn(block.note, scope, root)}
              </p>
            ) : undefined
          }
        >
          <Blocks blocks={block.blocks} scope={scope} root={root} />
        </Section>
      );
    }
    case "table":
      return <TableView block={block} scope={scope} root={root} />;
    case "pagination":
      return <PaginationView block={block} scope={scope} root={root} />;
    case "text": {
      const tone = valueIn(block.tone, scope, root);
      const size = (block as { size?: string }).size === "xs" ? "text-xs" : "text-sm";
      return <p className={cn(size, toneClass(tone))}>{textIn(block.content, scope, root)}</p>;
    }
    case "widget":
      return <EtherWidget name={block.widget} props={resolveProps(block.props, scope, root)} />;
    case "grid": {
      const className = cn("grid gap-4 sm:grid-cols-2 lg:grid-cols-3", block.className);
      const inner = <Blocks blocks={block.blocks} scope={scope} root={root} nested />;
      return block.as === "section" ? (
        <section className={className}>{inner}</section>
      ) : (
        <div className={className}>{inner}</div>
      );
    }
    case "heading":
      return block.level === 2 ? (
        <h2 className="text-base font-semibold text-fg">{textIn(block.content, scope, root)}</h2>
      ) : (
        <h3 className="text-sm font-semibold text-fg">{textIn(block.content, scope, root)}</h3>
      );
    case "panel":
      return (
        <Section
          title={textIn(block.title, scope, root)}
          description={textIn(block.hint, scope, root) || undefined}
        >
          <Blocks blocks={block.blocks} scope={scope} root={root} />
        </Section>
      );
    case "stat-tile": {
      const Icon = STAT_ICONS[textIn(block.iconKey, scope, root)];
      const tone = valueIn(block.tone, scope, root);
      return (
        <div className="rounded-xl border border-border bg-surface px-4 py-3">
          <p className="flex items-center gap-1.5 text-xs text-fg-muted">
            {Icon ? <Icon size={13} aria-hidden /> : null}
            {textIn(block.label, scope, root)}
          </p>
          <p className={cn("mt-1 text-xl font-semibold tabular-nums", toneClass(tone))}>
            {textIn(block.value, scope, root)}
          </p>
          {block.sub ? (
            <p className="mt-0.5 text-xs text-fg-muted">{textIn(block.sub, scope, root)}</p>
          ) : null}
        </div>
      );
    }
    case "repeat": {
      const items = rowsIn(block.items, scope, root);
      if (items.length === 0) {
        return block.empty ? (
          <p className="text-sm text-fg-muted">{textIn(block.empty.text, scope, root)}</p>
        ) : null;
      }
      return (
        <div className="space-y-4">
          {items.map((item, i) => {
            const key = resolveIn(item, root, block.itemKey);
            const inner: ReactNode = <Blocks blocks={block.blocks} scope={item} root={root} />;
            return block.unwrapped ? (
              <Fragment key={key != null && key !== "" ? String(key) : i}>{inner}</Fragment>
            ) : (
              <div key={key != null && key !== "" ? String(key) : i}>{inner}</div>
            );
          })}
        </div>
      );
    }
    case "frame":
      return (
        <EtherFrame name={block.frame} props={resolveProps(block.props, scope, root)}>
          <Blocks blocks={block.blocks} scope={scope} root={root} />
        </EtherFrame>
      );
  }
}

/** A list of blocks in one scope. Nested lists keep their parent's spacing. */
function Blocks({
  blocks,
  scope,
  root,
  nested = false,
}: {
  blocks: Block[];
  scope: unknown;
  root: unknown;
  nested?: boolean;
}) {
  if (nested) {
    return (
      <>
        {blocks.map((block, i) => (
          <BlockView key={i} block={block} scope={scope} root={root} />
        ))}
      </>
    );
  }
  return (
    <div className="space-y-4">
      {blocks.map((block, i) => (
        <BlockView key={i} block={block} scope={scope} root={root} />
      ))}
    </div>
  );
}

/** Render a validated plugin page document against its loader data. */
export function ViewSpecPage({ spec, data }: { spec: PageSpec; data: unknown }) {
  const root: unknown = data ?? {};
  return (
    <div className="space-y-6" data-testid="viewspec-page">
      {spec.header.length ? <Blocks blocks={spec.header} scope={root} root={root} /> : null}
      <Blocks blocks={spec.body} scope={root} root={root} />
    </div>
  );
}
