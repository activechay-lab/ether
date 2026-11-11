import { Badge, Button, Select } from "@braedonsaunders/appkit-ui";
import { ArrowDown, ArrowUp, X } from "lucide-react";
import { Field } from "../../../components/fields";
import { t } from "../../../i18n";
import type { ChannelLook } from "./model";
import {
  RUNDOWN_MOMENTS,
  effectiveAfterBreak,
  effectiveBeforeBreak,
  effectiveBetweenProgrammes,
  isRundownMoment,
  rundownMomentLabel,
  type RundownMoment,
} from "./rundown";

function SequenceRow({
  label,
  items,
  customized,
  allowed,
  onChange,
  onReset,
}: {
  label: string;
  items: RundownMoment[];
  customized: boolean;
  allowed: RundownMoment[];
  onChange: (next: RundownMoment[]) => void;
  onReset: () => void;
}) {
  const remaining = allowed.filter((m) => !items.includes(m));
  const move = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= items.length) return;
    const next = [...items];
    const [picked] = next.splice(index, 1);
    if (!picked) return;
    next.splice(target, 0, picked);
    onChange(next);
  };
  return (
    <Field label={label}>
      <div className="space-y-2 rounded-lg border border-border bg-surface p-2.5">
        {items.length > 0 ? (
          <div className="flex flex-wrap items-center gap-1.5">
            {items.map((kind, i) => (
              <div
                key={kind}
                className="inline-flex items-center gap-1 rounded-md border border-border bg-bg-subtle px-2 py-1 text-xs"
              >
                <Badge>{i + 1}</Badge>
                <span className="font-medium">{rundownMomentLabel(kind)}</span>
                <button
                  type="button"
                  aria-label={t("Move up")}
                  disabled={i === 0}
                  onClick={() => move(i, -1)}
                  className="rounded p-0.5 text-fg-muted hover:text-fg disabled:opacity-30"
                >
                  <ArrowUp size={12} />
                </button>
                <button
                  type="button"
                  aria-label={t("Move down")}
                  disabled={i === items.length - 1}
                  onClick={() => move(i, 1)}
                  className="rounded p-0.5 text-fg-muted hover:text-fg disabled:opacity-30"
                >
                  <ArrowDown size={12} />
                </button>
                <button
                  type="button"
                  aria-label={t("Remove")}
                  onClick={() => onChange(items.filter((_, n) => n !== i))}
                  className="rounded p-0.5 text-fg-muted hover:text-danger"
                >
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-xs text-fg-muted">{t("Off")}</p>
        )}
        <div className="flex flex-wrap items-center gap-2">
          {remaining.length > 0 ? (
            <Select
              aria-label={t("Add moment")}
              value=""
              onChange={(e) => {
                const v = e.currentTarget.value;
                if (isRundownMoment(v)) {
                  onChange([...items, v]);
                }
              }}
            >
              <option value="">{t("Add moment")}</option>
              {remaining.map((kind) => (
                <option key={kind} value={kind}>
                  {rundownMomentLabel(kind)}
                </option>
              ))}
            </Select>
          ) : null}
          {customized ? (
            <Button variant="ghost" size="sm" onClick={onReset}>
              {t("Reset")}
            </Button>
          ) : null}
        </div>
      </div>
    </Field>
  );
}

export function RundownOrderEditor({
  value,
  onChange,
  includeBetween = true,
}: {
  value?: ChannelLook;
  onChange: (patch: Partial<ChannelLook>) => void;
  includeBetween?: boolean;
}) {
  const applyList = (
    field: "beforeBreak" | "afterBreak" | "betweenProgrammes",
    next: RundownMoment[],
  ) => {
    const patch: Partial<ChannelLook> = { [field]: next };
    if (next.includes("tonight")) patch.tonight = true;
    if (next.includes("upnext")) patch.upNext = true;
    onChange(patch);
  };
  const hasBreaks = !!value?.breaks || !!value?.breakPattern?.inside;
  return (
    <div className="space-y-3">
      {hasBreaks ? (
        <>
          <SequenceRow
            label={t("Before a break")}
            items={effectiveBeforeBreak(value)}
            customized={value?.beforeBreak !== undefined}
            allowed={["tonight", "upnext", "ident", "break"]}
            onChange={(next) => applyList("beforeBreak", next)}
            onReset={() => onChange({ beforeBreak: undefined })}
          />
          <SequenceRow
            label={t("After a break")}
            items={effectiveAfterBreak(value)}
            customized={value?.afterBreak !== undefined}
            allowed={["back", "ident", "upnext", "tonight"]}
            onChange={(next) => applyList("afterBreak", next)}
            onReset={() => onChange({ afterBreak: undefined })}
          />
        </>
      ) : null}
      {includeBetween ? (
        <SequenceRow
          label={t("Between programmes")}
          items={effectiveBetweenProgrammes(value)}
          customized={value?.betweenProgrammes !== undefined}
          allowed={RUNDOWN_MOMENTS.filter((m) => m !== "break" && m !== "back")}
          onChange={(next) => applyList("betweenProgrammes", next)}
          onReset={() => onChange({ betweenProgrammes: undefined })}
        />
      ) : null}
    </div>
  );
}
