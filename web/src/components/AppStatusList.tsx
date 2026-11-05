import { copyText } from "./CopyButton";
import { Button, Tooltip } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, CheckCircle2, XCircle, Copy, CircleHelp } from "lucide-react";
import type { ComponentProps } from "react";
import { SettingsRow } from "./SettingsRow";
import { t } from "../i18n";

export function StatusRow({
  state,
  title,
  ...props
}: ComponentProps<typeof SettingsRow> & { state?: string }) {
  const Icon =
    state === "reachable" ? CheckCircle2 : state === "unreachable" ? XCircle : AlertTriangle;
  const color =
    state === "reachable"
      ? "text-success"
      : state === "unreachable"
        ? "text-danger"
        : "text-warning";
  return (
    <SettingsRow
      {...props}
      title={
        <span className="flex items-center gap-3">
          <Icon className={color} size={20} aria-hidden />
          {title}
        </span>
      }
    />
  );
}

export function AddressChip({ address }: { address: string }) {
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-lg border border-border bg-bg-subtle px-3 py-2">
      <a href={address} className="break-all font-mono text-sm text-primary">
        {address}
      </a>
      <Button
        variant="ghost"
        size="sm"
        aria-label={t("Copy address")}
        onClick={() => {
          void copyText(address);
        }}
      >
        <Copy size={16} />
      </Button>
    </div>
  );
}

export function StatusHelp({ text }: { text: string }) {
  return (
    <Tooltip content={text} className="max-w-xs whitespace-normal">
      <button
        type="button"
        aria-label={t("More information")}
        className="inline-flex text-fg-muted"
      >
        <CircleHelp size={15} />
      </button>
    </Tooltip>
  );
}
export function ConnectionStatusRow({
  state,
  title,
  detail,
  address,
}: {
  state?: string;
  title: string;
  detail: string;
  address?: string;
}) {
  const label =
    state === "reachable"
      ? t("Reachable")
      : state === "unreachable"
        ? t("Can't reach it")
        : t("Not checked yet");
  return (
    <StatusRow
      state={state}
      title={
        <span className="flex flex-wrap items-center gap-2">
          {title}
          <span className="font-normal text-fg-muted">· {label}</span>
          <StatusHelp text={detail} />
        </span>
      }
      control={address ? <AddressChip address={address} /> : undefined}
    />
  );
}
