import { cn } from "@braedonsaunders/appkit-ui";
import type { ReactNode } from "react";

type KpiTone = "primary" | "info" | "success" | "warning" | "danger";

const TONE: Record<KpiTone, string> = {
  primary: "bg-primary-subtle text-primary",
  info: "bg-info-subtle text-info",
  success: "bg-success-subtle text-success",
  warning: "bg-warning-subtle text-warning",
  danger: "bg-danger-subtle text-danger",
};

/**
 * The console's one KPI tile. Every headline number in the app uses it, so
 * they all read the same: label, value, an optional detail line, and an
 * optional icon whose chip carries the tone. No coloured edge strip.
 */
export function Kpi({
  label,
  value,
  detail,
  icon,
  tone = "primary",
  className,
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  icon?: ReactNode;
  tone?: KpiTone;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "h-full min-w-0 overflow-hidden rounded-xl border border-border bg-surface p-4 shadow-xs",
        className,
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-xs font-semibold uppercase tracking-wider text-fg-subtle">
            {label}
          </div>
          <div className="mt-3 truncate text-2xl font-semibold tabular-nums text-fg">{value}</div>
          {/* The detail line is always reserved, so every KPI row is the same height. */}
          <div className="mt-1 h-4 truncate text-xs text-fg-muted">{detail}</div>
        </div>
        {icon ? (
          <span
            className={cn(
              // Phones keep the width for the value; the chip only decorates.
              "hidden size-9 shrink-0 place-items-center rounded-lg sm:grid",
              TONE[tone],
            )}
            aria-hidden
          >
            {icon}
          </span>
        ) : null}
      </div>
    </div>
  );
}

/** A row of KPI tiles: two across on small screens, four on wide ones. */
export function KpiRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("grid grid-cols-2 gap-3 lg:grid-cols-4", className)}>{children}</div>;
}
