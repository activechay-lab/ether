import { Button, cn } from "@braedonsaunders/appkit-ui";
import { X } from "lucide-react";
import { useContext, useEffect, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { t } from "../i18n";
import { formatNumber } from "../lib/format";
import { WorkspaceOverlayContext } from "./workspace";

const OPEN_OVERLAY_SELECTOR =
  '[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"], dialog[open]';

/**
 * Shared floating bottom bulk-action banner for studio workspaces (Home, Live TV).
 * Portals to the enclosing Workspace bottom overlay when `floating` is true so
 * selecting items never shifts list or table layout.
 */
export function BulkBar({
  count,
  busy = false,
  label,
  leading,
  floating = true,
  className,
  onClear,
  children,
}: {
  count: number;
  busy?: boolean;
  label: string;
  /** Optional control shown next to the selection count before the divider (e.g. Select all matches). */
  leading?: ReactNode;
  /** When false, renders inline inside a caller-owned floating slot (such as GuideGrid). */
  floating?: boolean;
  className?: string;
  onClear: () => void;
  children: ReactNode;
}) {
  const overlay = useContext(WorkspaceOverlayContext);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented || busy) return;
      if (document.querySelector(OPEN_OVERLAY_SELECTOR)) return;
      event.preventDefault();
      onClear();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [busy, onClear]);

  const bar = (
    <div
      role="region"
      aria-label={label}
      aria-busy={busy}
      className={cn(
        "bulk-bar-enter pointer-events-auto flex max-w-full flex-wrap items-center justify-center gap-1.5 rounded-xl border border-border-strong bg-surface p-2 shadow-xl",
        className,
      )}
    >
      <span role="status" className="px-2 text-sm font-semibold tabular-nums">
        {busy ? t("Saving…") : t("{count} selected", { count: formatNumber(count) })}
      </span>
      {leading}
      <span className="mx-1 hidden h-5 w-px bg-border sm:block" aria-hidden />
      {children}
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        aria-label={t("Clear selection")}
        onClick={onClear}
      >
        <X size={15} />
        {t("Done")}
      </Button>
    </div>
  );

  if (!floating) return bar;
  if (overlay) return createPortal(bar, overlay);
  return (
    <div className="pointer-events-none fixed inset-x-3 bottom-3 z-40 flex justify-center">
      {bar}
    </div>
  );
}
