import { Play, CalendarDays, Zap } from "lucide-react";
import { Input, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { MoreOptions } from "../../components/ConfigSection";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tn, tx } from "../../i18n";
import { api, apiFetch } from "../../lib/api";
import { formatNumber, relativeTime } from "../../lib/format";
import { useAsync, useInterval } from "../../lib/hooks";
import {
  applyReleaseMode,
  customRequestCeiling,
  releaseMode,
  type ReleaseMode,
} from "../../lib/release-mode";
import type { AvailabilityConfig } from "../../lib/types";

interface Summary {
  ready: number;
  waiting: number;
  lastChecked?: number;
}

const MODES: {
  value: ReleaseMode;
  recommended?: boolean;
  title: () => string;
  body: () => string;
}[] = [
  {
    value: "playable",
    recommended: true,
    title: () => t("When they can play"),
    body: () =>
      t(
        "A new movie appears once Ether finds a release you can play with your quality settings. Ether checks for you automatically.",
      ),
  },
  {
    value: "digital",
    title: () => t("On digital release"),
    body: () =>
      t("Appears on its digital or physical release date. No checking, but some may not play yet."),
  },
  {
    value: "immediate",
    title: () => t("Right away"),
    body: () => t("Every new movie shows immediately, even if it's only in theatres."),
  },
];

/** The three release modes share one availability setting. */
export function NewReleasesSettings({
  availability,
  onAvailability,
}: {
  availability: AvailabilityConfig;
  onAvailability: (next: AvailabilityConfig) => void;
}) {
  const summary = useAsync((signal) => apiFetch<Summary>("/availability/summary", { signal }), []);
  useInterval(() => void summary.reload(), 30000);
  const mode = releaseMode(availability);

  return (
    <SettingsSection title={t("New releases")}>
      <div className="new-release-modes" role="radiogroup" aria-label={t("New releases")}>
        {MODES.map((item) => {
          const selected = mode === item.value;
          const Icon =
            item.value === "playable" ? Play : item.value === "digital" ? CalendarDays : Zap;
          return (
            <div key={item.value} className="new-release-mode">
              <button
                type="button"
                role="radio"
                aria-checked={selected}
                className={`new-release-card${selected ? " is-selected" : ""}`}
                onClick={() => {
                  onAvailability(applyReleaseMode(availability, item.value));
                }}
              >
                <Icon className="new-release-card-icon" aria-hidden="true" />
                <span className="new-release-card-head">
                  <span
                    data-setting={
                      {
                        playable: "when-they-can-play",
                        digital: "on-digital-release",
                        immediate: "right-away",
                      }[item.value]
                    }
                    className="new-release-card-title"
                  >
                    {item.title()}
                  </span>
                  {item.recommended ? (
                    <span className="new-release-recommended">{t("Recommended")}</span>
                  ) : null}
                </span>
                <span className="new-release-card-body">{item.body()}</span>
              </button>
            </div>
          );
        })}
      </div>
      {mode === "playable" && summary.data ? (
        <p className="new-release-status">
          {tx("{ready} ready · {waiting} · checked {when}", {
            ready: formatNumber(summary.data.ready),
            waiting: (
              <Link to="/catalog?waiting=1&type=movie" className="text-primary underline">
                {tn(
                  summary.data.waiting,
                  "{count} waiting for a release",
                  "{count} waiting for a release",
                )}
              </Link>
            ),
            when: summary.data.lastChecked
              ? relativeTime(new Date(summary.data.lastChecked * 1000))
              : t("not yet"),
          })}
        </p>
      ) : null}
    </SettingsSection>
  );
}

/**
 * Daily indexer budget for new-release checks (`availability.effort` /
 * `feedRequests`), edited on Sources › Indexers.
 */
function NewReleaseBudget({
  availability,
  onChange,
}: {
  availability: AvailabilityConfig;
  onChange: (next: AvailabilityConfig) => void;
}) {
  const ceiling = customRequestCeiling(availability);
  return (
    <SettingsRow
      setting="daily-budget-for-new-release-checks"
      title={t("Daily budget for new-release checks")}
      description={t("Leave empty to let Ether choose a daily limit from your indexers.")}
      control={
        <Input
          aria-label={t("Daily budget for new-release checks")}
          type="number"
          inputMode="numeric"
          min={0}
          max={100000}
          className="w-28 tabular-nums"
          value={ceiling ?? ""}
          onChange={(e) => {
            const raw = e.currentTarget.value.trim();
            const value = raw === "" ? 0 : Number(raw);
            if (!Number.isFinite(value) || value <= 0) {
              if (ceiling !== null)
                onChange({ ...availability, effort: "normal", feedRequests: 400 });
              return;
            }
            onChange({ ...availability, effort: "custom", feedRequests: Math.round(value) });
          }}
        />
      }
    />
  );
}

/** The `availability` section (new-release modes, budget) as a draft with its own save. */
export function useAvailabilityDraft() {
  const config = useAsync(() => api.getConfig(), []);
  const stored = (config.data?.availability ?? null) as AvailabilityConfig | null;
  const { draft, setDraft, dirty, reset } = useDraft<AvailabilityConfig>(stored);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      await api.putSection("availability", draft);
      toast.success(t("Settings saved"));
      await config.reload();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  const saveBar = (
    <SaveBar
      dirty={dirty}
      saving={saving}
      error={error}
      onSave={() => void save()}
      onDiscard={() => {
        reset();
        setError(null);
      }}
    />
  );
  return { draft, setDraft, saveBar };
}

/** Sources › Indexers: the new-release check budget, under More options. */
export function NewReleaseBudgetSection() {
  const availability = useAvailabilityDraft();
  if (!availability.draft) return null;
  return (
    <SettingsSection title={t("Daily budgets")}>
      <MoreOptions
        setting="daily-budgets"
        label={t("More options: {title}", { title: t("Daily budgets") })}
      >
        <NewReleaseBudget availability={availability.draft} onChange={availability.setDraft} />
      </MoreOptions>
      {availability.saveBar}
    </SettingsSection>
  );
}
