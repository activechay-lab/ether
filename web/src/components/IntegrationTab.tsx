import { Badge, Switch } from "@braedonsaunders/appkit-ui";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { IntegrationMark } from "./integration-brands";
import { t } from "../i18n";

/** Shared account workspace; feature choices remain at the linked destination. */
export function IntegrationTab({
  id,
  name,
  enabled,
  ready = true,
  onToggle,
  disabled,
  test,
  href,
  usage,
  current,
  children,
}: {
  id: string;
  name: string;
  enabled: boolean;
  ready?: boolean;
  onToggle?: (on: boolean) => void;
  disabled?: boolean;
  test?: ReactNode;
  href?: string;
  usage?: string;
  current?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-center gap-4 rounded-xl border border-border bg-surface p-5">
        <div className="flex size-14 shrink-0 items-center justify-center rounded-xl bg-bg-subtle">
          <IntegrationMark id={id} />
        </div>
        <div className="min-w-0 flex-1 space-y-1.5">
          <h2 className="text-lg font-semibold text-fg">{name}</h2>
          <Badge variant={!enabled ? "secondary" : ready ? "success" : "warning"}>
            {!enabled ? t("Off") : ready ? t("Connected") : t("Needs a key")}
          </Badge>
        </div>
        <div className="flex w-full items-center justify-end gap-3 sm:w-auto">
          {test}
          {onToggle && (
            <Switch
              aria-label={t("{app} account enabled", { app: name })}
              checked={enabled}
              disabled={disabled}
              onChange={(e) => onToggle(e.currentTarget.checked)}
            />
          )}
        </div>
      </header>
      <div className="space-y-4">{children}</div>
      {href && usage ? (
        <div className="flex flex-wrap items-center gap-2 text-sm text-fg-muted">
          <Link to={href} className="text-primary hover:underline">
            {usage}
          </Link>
          {current !== undefined && (
            <Badge variant={current ? "success" : "secondary"}>
              {current ? t("Current source") : t("Not selected")}
            </Badge>
          )}
        </div>
      ) : null}
    </div>
  );
}
