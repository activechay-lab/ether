import {
  PageContainer,
  PageHeader,
  SubtabNav as AppKitSubtabNav,
  cn,
} from "@braedonsaunders/appkit-ui";
import { useRef, type ReactNode, type ComponentProps, type RefObject } from "react";
import { OverflowTabs } from "./OverflowTabs";
import { useNavigate, useParams } from "react-router-dom";
import { t } from "../i18n";

export interface PageTab {
  key: string;
  label: string;
  count?: number;
}

/**
 * The active tab of a domain page, read from the `:tab` route segment so every
 * tab is deep-linkable (search, health links, notifications all land on it).
 */
export function useRouteTab<K extends string>(tabs: readonly { key: K }[], fallback?: K): K {
  const { tab } = useParams();
  const match = tabs.find((x) => x.key === tab);
  return (match?.key ?? fallback ?? tabs[0]!.key) as K;
}

/**
 * One layout for every multi-section page: header, optional live status strip,
 * URL-backed tabs, then the active tab's content edited in place.
 */
export function DomainPage({
  title,
  description,
  actions,
  status,
  base,
  tabs,
  active,
  tabActions,
  tabHref,
  children,
  className,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  /** Controls for the active tab (filters), on the right of the tab row. */
  tabActions?: ReactNode;
  /** Live state shown above the tabs (health, budgets, now playing). */
  status?: ReactNode;
  /** Route prefix; tabs navigate to `${base}/${key}`. */
  base: string;
  /** Where a tab navigates instead, e.g. to carry a period shared by two tabs. */
  tabHref?: (key: string) => string;
  tabs?: readonly PageTab[];
  active?: string;
  children: ReactNode;
  className?: string;
}) {
  const navigate = useNavigate();
  const tabsRoot = useRef<HTMLDivElement>(null);
  return (
    <PageContainer className={cn("space-y-6", className)}>
      <PageHeader title={title} description={description} actions={actions} />
      {status}
      {tabs && tabs.length > 1 ? (
        <div
          ref={tabsRoot}
          className="flex flex-wrap items-end gap-x-4 gap-y-2 border-b border-border"
        >
          {/* Narrow screens wrap the tab actions onto their own line before the tabs collapse. */}
          <div className="min-w-[min(100%,14rem)] flex-1">
            <OverflowTabs
              ariaLabel={t("{page} sections", { page: title })}
              active={active ?? tabs[0]!.key}
              onSelect={(key) =>
                keepScroll(tabsRoot, () => navigate(tabHref?.(key) ?? `${base}/${key}`))
              }
              tabs={tabs.map((x) => ({ key: x.key, label: x.label, count: x.count }))}
            />
          </div>
          {tabActions ? (
            <div className="flex max-w-full shrink-0 flex-wrap items-center gap-2 pb-2">
              {tabActions}
            </div>
          ) : null}
        </div>
      ) : null}
      <div className="space-y-6 pb-4">{children}</div>
    </PageContainer>
  );
}

/**
 * A titled block of in-place content. Use AppKit `SettingsSection` +
 * `SettingsRow` for label/control rows; use this for free-form content (lists,
 * previews, tools) that still needs the same card chrome.
 */
export function Section({
  title,
  description,
  actions,
  footer,
  children,
  className,
  bodyClassName,
  id,
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  footer?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  id?: string;
}) {
  return (
    <section
      id={id}
      aria-label={typeof title === "string" ? title : undefined}
      className={cn("overflow-hidden rounded-xl border border-border bg-surface", className)}
    >
      {title || description || actions ? (
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-5 py-4">
          <div className="min-w-0">
            {title ? <h2 className="text-sm font-semibold text-fg">{title}</h2> : null}
            {description ? (
              <p className="mt-0.5 max-w-2xl text-sm text-fg-muted">{description}</p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
          ) : null}
        </div>
      ) : null}
      <div className={bodyClassName ?? "space-y-4 px-5 py-4"}>{children}</div>
      {footer ? (
        <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border bg-bg-subtle px-5 py-3">
          {footer}
        </div>
      ) : null}
    </section>
  );
}

// All page tabs share spacing and retain their scroll container during selection.
/**
 * Run a tab switch without moving the viewport: every scroll ancestor of
 * `root` keeps its offset, and Chrome's scroll anchoring is turned off on the
 * app scroller so a shorter panel can't pull the page to the top.
 */
function keepScroll(root: RefObject<HTMLElement | null>, change: () => void) {
  const positions: [HTMLElement, number][] = [];
  let node: HTMLElement | null = root.current;
  while (node) {
    if (node.classList.contains("app-scroll")) node.style.overflowAnchor = "none";
    positions.push([node, node.scrollTop]);
    node = node.parentElement;
  }
  change();
  requestAnimationFrame(() => {
    for (const [element, top] of positions) element.scrollTop = top;
  });
}

export function SubtabNav(props: ComponentProps<typeof AppKitSubtabNav>) {
  const root = useRef<HTMLDivElement>(null);
  return (
    <div ref={root} className="mb-6">
      <AppKitSubtabNav
        {...props}
        onSelect={(key) => keepScroll(root, () => props.onSelect?.(key))}
      />
    </div>
  );
}

/** URL-backed sections within a page, on the shared sub-tab template. */
export function PageSubtabs({
  active,
  onSelect,
  tabs,
  ariaLabel,
}: {
  active: string;
  onSelect: (key: string) => void;
  tabs: PageTab[];
  ariaLabel: string;
}) {
  return <SubtabNav active={active} onSelect={onSelect} tabs={tabs} ariaLabel={ariaLabel} />;
}
