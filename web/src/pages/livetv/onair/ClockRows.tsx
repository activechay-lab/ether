import { Link } from "react-router-dom";
import { textOf, useRegistry } from "../../../lib/extend";
import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import { Plus, Trash2, CloudSun } from "lucide-react";
import { Field } from "../../../components/fields";
import { t } from "../../../i18n";
import { timeOfDay, minutesOfDay } from "../model";
import type { ClockSlot } from "./model";
export function ClockRows({
  slots,
  onChange,
}: {
  slots: ClockSlot[];
  onChange: (slots: ClockSlot[]) => void;
}) {
  const segments = useRegistry<{ duration: number }>("onairSegment");
  return (
    <section className="space-y-3 rounded-xl border border-border bg-surface p-4">
      <div className="flex items-center justify-between">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-fg">
          <CloudSun size={16} className="text-primary" />
          {t("On the clock")}
        </h3>
        <Button
          disabled={!segments.descriptors.length}
          size="sm"
          variant="outline"
          onClick={() =>
            onChange([
              ...slots,
              {
                segmentId: segments.descriptors[0]?.id ?? "",
                minute: 58,
                everyMinutes: 60,
                from: 360,
                until: 1380,
                days: [],
              },
            ])
          }
        >
          <Plus size={14} />
          {t("Add segment")}
        </Button>
      </div>
      {slots.map((slot, i) => {
        const set = (patch: Partial<ClockSlot>) =>
          onChange(slots.map((v, n) => (n === i ? { ...v, ...patch } : v)));
        const owner = segments.descriptors.find((s) => s.id === slot.segmentId)?.owner;
        return (
          <div key={i} className="space-y-3 rounded-lg border border-border p-3">
            <p className="text-sm font-medium text-fg">
              {t("{name} at :{minute} every {every} minutes, {from} to {until}", {
                name:
                  textOf(segments.descriptors.find((s) => s.id === slot.segmentId)?.label) ||
                  slot.segmentId,
                every: slot.everyMinutes || 60,
                minute: String(slot.minute).padStart(2, "0"),
                from: timeOfDay(slot.from),
                until: timeOfDay(slot.until),
              })}
            </p>
            <div className="flex flex-wrap items-end gap-3">
              <Field label={t("From plugins")}>
                <Select
                  aria-label={t("Clock segment")}
                  value={slot.segmentId}
                  onChange={(e) => set({ segmentId: e.currentTarget.value })}
                >
                  {segments.descriptors.map((segment) => (
                    <option key={segment.id} value={segment.id}>
                      {t("{name} · {plugin} · {minutes} min", {
                        name: textOf(segment.label),
                        plugin: segment.group ?? "",
                        minutes: (segment.meta?.duration ?? 0) / 60,
                      })}
                    </option>
                  ))}
                </Select>
              </Field>
              {owner ? (
                <Link
                  className="text-sm text-primary hover:underline"
                  to={`/plugins/installed?plugin=${encodeURIComponent(owner)}&tab=setup`}
                >
                  {t("Plugin settings")}
                </Link>
              ) : null}
              <Field label={t("Minute")}>
                <Input
                  type="number"
                  min={0}
                  max={59}
                  className="w-20"
                  value={slot.minute}
                  onChange={(e) =>
                    set({ minute: Math.max(0, Math.min(59, Number(e.currentTarget.value))) })
                  }
                />
              </Field>
              <Field label={t("From")}>
                <Input
                  type="time"
                  value={timeOfDay(slot.from)}
                  onChange={(e) => set({ from: minutesOfDay(e.currentTarget.value) })}
                />
              </Field>
              <Field label={t("Until")}>
                <Input
                  type="time"
                  value={timeOfDay(slot.until)}
                  onChange={(e) => set({ until: minutesOfDay(e.currentTarget.value) })}
                />
              </Field>
              <Button
                size="sm"
                variant="ghost"
                aria-label={t("Remove clock segment")}
                onClick={() => onChange(slots.filter((_, n) => n !== i))}
              >
                <Trash2 size={15} />
              </Button>
            </div>
          </div>
        );
      })}
      {!slots.length ? (
        <p className="text-xs text-fg-muted">
          {t("Schedule a plugin segment at the same minute each hour.")}
        </p>
      ) : null}
    </section>
  );
}
