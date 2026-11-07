import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { cn, toast } from "@braedonsaunders/appkit-ui";
import { Scale, ShieldCheck, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { api, errorMessage } from "../../lib/api";
import type { Stability } from "./model";

/** The stability presets, in the order owners choose between them. */
export const STABILITY: {
  value: Stability;
  title: () => string;
  body: () => string;
  icon: typeof Zap;
}[] = [
  {
    value: "fastest",
    title: () => t("Fastest"),
    body: () =>
      t("Closest to live. When the provider hiccups you may see a brief reconnecting screen."),
    icon: Zap,
  },
  {
    value: "balanced",
    title: () => t("Balanced"),
    body: () =>
      t(
        "Keeps a few seconds of the channel in hand whenever the provider allows, so most hiccups pass unnoticed.",
      ),
    icon: Scale,
  },
  {
    value: "rocksolid",
    title: () => t("Rock solid"),
    body: () =>
      t(
        "Keeps the most in hand and smooths every channel it can, so even longer outages ride through. Uses more of this server's processing power.",
      ),
    icon: ShieldCheck,
  },
];

export function stabilityTitle(value: Stability): string {
  return (STABILITY.find((s) => s.value === value) ?? STABILITY[1]!).title();
}

/** The server-wide stability choice; each channel can override it. */
export function StabilityPanel() {
  const [value, setValue] = useState<Stability | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    const ctl = new AbortController();
    api
      .getSection<{ stability?: Stability | "" }>("livetv")
      .then((r) => {
        if (!ctl.signal.aborted) setValue(r.value?.stability || "balanced");
      })
      .catch(() => {
        if (!ctl.signal.aborted) setValue("balanced");
      });
    return () => ctl.abort();
  }, []);

  const choose = async (next: Stability) => {
    if (next === value || saving) return;
    const previous = value;
    setValue(next);
    setSaving(true);
    try {
      await api.putSection("livetv", { stability: next });
      toast.success(t("Stream stability set to {name}", { name: stabilityTitle(next) }), {
        description: t("Applies the next time a channel starts."),
      });
    } catch (e) {
      setValue(previous);
      toast.error(t("Couldn't change stream stability"), { description: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };

  return (
    <DashboardPanel title={t("Stream stability")} icon={<ShieldCheck size={16} />}>
      <p className="mb-3 text-xs text-fg-muted">
        {t(
          "How much of each channel Ether keeps in hand, so provider hiccups don't reach the TV. Channels can choose their own.",
        )}
      </p>
      <div
        role="radiogroup"
        aria-label={t("Stream stability")}
        aria-busy={value === null || saving}
        className="grid gap-3 lg:grid-cols-3"
      >
        {STABILITY.map((item) => {
          const selected = value === item.value;
          const Icon = item.icon;
          return (
            <button
              key={item.value}
              type="button"
              role="radio"
              aria-checked={selected}
              disabled={value === null}
              onClick={() => void choose(item.value)}
              className={cn(
                "flex flex-col items-start gap-1.5 rounded-xl border p-3 text-left transition-colors",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/50",
                selected
                  ? "border-primary bg-primary/5"
                  : "border-border bg-surface hover:border-border-strong",
              )}
            >
              <span className="flex items-center gap-2">
                <Icon
                  size={16}
                  aria-hidden="true"
                  className={selected ? "text-primary" : "text-fg-muted"}
                />
                <span className="text-sm font-medium text-fg">{item.title()}</span>
                {item.value === "balanced" ? (
                  <span className="text-xs text-fg-muted">{t("Recommended")}</span>
                ) : null}
              </span>
              <span className="text-xs text-fg-muted">{item.body()}</span>
            </button>
          );
        })}
      </div>
    </DashboardPanel>
  );
}
