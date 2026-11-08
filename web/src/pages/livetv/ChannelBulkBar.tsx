import { Button, Input, Popover } from "@braedonsaunders/appkit-ui";
import { FolderInput, Power, PowerOff, Star, StarOff, X } from "lucide-react";
import { useId, useState } from "react";
import { t } from "../../i18n";
import { formatNumber } from "../../lib/format";

export type ChannelPatch = {
  enabled?: boolean;
  favorite?: boolean;
  group?: string;
  plexHidden?: boolean;
};
export type ChannelSelection = {
  ids: ReadonlySet<string>;
  busy: boolean;
  all: boolean;
  toggle: (id: string, range?: boolean) => void;
  toggleAll: () => void;
};

/** Floats over the guide; selecting rows never resizes its header or columns. */
export function ChannelBulkBar({
  count,
  total,
  busy,
  groups,
  onAll,
  onClear,
  onApply,
}: {
  count: number;
  total: number;
  busy: boolean;
  groups: string[];
  onAll: () => void;
  onClear: () => void;
  onApply: (patch: ChannelPatch) => Promise<boolean>;
}) {
  const [groupOpen, setGroupOpen] = useState(false);
  const [group, setGroup] = useState("");
  const listId = useId();
  return (
    <div
      role="region"
      aria-label={t("Channel actions")}
      aria-busy={busy}
      className="pointer-events-auto flex max-w-full flex-wrap items-center justify-center gap-1.5 rounded-xl border border-border-strong bg-surface p-2 shadow-xl"
    >
      <span role="status" className="px-2 text-sm font-semibold tabular-nums">
        {busy ? t("Saving…") : t("{count} selected", { count: formatNumber(count) })}
      </span>
      {count < total ? (
        <Button size="sm" variant="ghost" disabled={busy} onClick={onAll}>
          {t("Select all matches")}
        </Button>
      ) : null}
      <span className="mx-1 hidden h-5 w-px bg-border sm:block" aria-hidden />
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ enabled: true })}
      >
        <Power size={14} />
        {t("Enable")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ enabled: false })}
      >
        <PowerOff size={14} />
        {t("Disable")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ favorite: true })}
      >
        <Star size={14} />
        {t("Favourite")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ favorite: false })}
      >
        <StarOff size={14} />
        {t("Unfavourite")}
      </Button>
      <Popover
        open={groupOpen}
        onOpenChange={setGroupOpen}
        align="end"
        side="top"
        className="w-72 p-3"
        trigger={
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => setGroupOpen(!groupOpen)}
          >
            <FolderInput size={14} />
            {t("Move to group")}
          </Button>
        }
      >
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault();
            if (await onApply({ group: group.trim() })) setGroupOpen(false);
          }}
        >
          <label className="block space-y-1.5 text-sm font-medium">
            <span>{t("Group")}</span>
            <Input
              autoFocus
              value={group}
              maxLength={200}
              disabled={busy}
              list={listId}
              onChange={(e) => setGroup(e.currentTarget.value)}
            />
          </label>
          <datalist id={listId}>
            {groups.map((name) => (
              <option key={name} value={name} />
            ))}
          </datalist>
          <Button type="submit" size="sm" disabled={busy || !group.trim()}>
            {t("Save")}
          </Button>
        </form>
      </Popover>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ plexHidden: false })}
      >
        {t("Show in Plex")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ plexHidden: true })}
      >
        {t("Hide from Plex")}
      </Button>
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
}
