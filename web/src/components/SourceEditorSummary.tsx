import type { ReactNode } from "react";

/** The source editor header and status strip shared by IPTV and account editors. */
export function SourceEditorHeader({
  icon,
  title,
  description,
  actions,
  children,
  tone = "primary",
}: {
  icon: ReactNode;
  tone?: "primary" | "info";
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="flex items-start gap-4">
      <span
        className={`grid size-12 shrink-0 place-items-center rounded-xl ${tone === "info" ? "bg-info/15 text-info" : "bg-primary-subtle text-primary"}`}
      >
        {icon}
      </span>
      <div className="min-w-0 flex-1">
        {children}
        <h2 className="truncate text-xl font-semibold tracking-tight text-fg">{title}</h2>
        <div className="text-sm text-fg-muted">{description}</div>
      </div>
      {actions}
    </div>
  );
}

export function SourceEditorMetrics({
  items,
}: {
  items: { key: string; label: ReactNode; value: ReactNode }[];
}) {
  return (
    <div
      className={`grid grid-cols-2 gap-2 ${items.length === 3 ? "sm:grid-cols-3" : "sm:grid-cols-4"}`}
    >
      {items.map(({ key, label, value }) => (
        <div key={key} className="rounded-xl border border-border bg-surface px-3 py-2.5">
          <p className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">{label}</p>
          <p className="mt-0.5 truncate text-lg font-semibold tabular-nums text-fg">{value}</p>
        </div>
      ))}
    </div>
  );
}
