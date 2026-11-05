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
  return (
    <section className="space-y-3 rounded-xl border border-border bg-surface p-4">
      <div className="flex items-center justify-between">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-fg">
          <CloudSun size={16} className="text-primary" />
          {t("On the clock")}
        </h3>
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            onChange([
              ...slots,
              {
                segmentId: "plugin:weather/forecast",
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
        return (
          <div key={i} className="space-y-3 rounded-lg border border-border p-3">
            <p className="text-sm font-medium text-fg">
              {t("Local forecast at :{minute} every hour, {from} to {until}", {
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
                  <option value="plugin:weather/forecast">
                    {t("Local forecast · Weather · 2 min")}
                  </option>
                </Select>
              </Field>
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
