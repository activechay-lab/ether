import { MultiSelect, Select, Switch, Tooltip } from "@braedonsaunders/appkit-ui";
import { Info } from "lucide-react";
import { Artwork } from "../../../components/Artwork";
import { MoreOptions } from "../../../components/ConfigSection";
import { SettingsRow } from "../../../components/SettingsRow";
import { Field, NumberInput } from "../../../components/fields";
import { t } from "../../../i18n";
import type { Programme } from "../model";
import type { CollectionChoice } from "../ChannelStudio";
import type { PersonalRecipe } from "./model";

export function InfoTip({ text }: { text: string }) {
  return (
    <Tooltip content={text} className="w-72 max-w-xs whitespace-normal">
      <button type="button" aria-label={text} className="inline-flex align-middle text-fg-subtle">
        <Info size={14} />
      </button>
    </Tooltip>
  );
}
export function RecipeTab({
  recipe,
  onChange,
  rundown,
  collections,
}: {
  recipe: PersonalRecipe;
  onChange: (recipe: PersonalRecipe) => void;
  rundown: Programme[];
  collections: CollectionChoice[];
}) {
  const labels: Record<string, string> = {
    continue: t("Continue watching"),
    nextEpisodes: t("Next episodes"),
    picked: t("Picked for them"),
    watchlist: t("From their watchlist"),
    comfort: t("Comfort rewatches"),
    newReleases: t("New releases they'd like"),
    collections: t("Your picks (collections)"),
  };
  const set = (patch: Partial<PersonalRecipe>) => onChange({ ...recipe, ...patch });
  const ingredient = (i: number, patch: Partial<PersonalRecipe["ingredients"][number]>) =>
    set({ ingredients: recipe.ingredients.map((v, n) => (n === i ? { ...v, ...patch } : v)) });
  return (
    <div className="space-y-5">
      <div className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-surface">
        {recipe.ingredients.map((v, i) => {
          const pick = rundown[i % Math.max(1, rundown.length)];
          return (
            <div key={v.kind} className="flex flex-wrap items-center gap-3 p-3 sm:flex-nowrap">
              <Artwork
                src={pick?.item.thumb}
                title={pick?.item.title || labels[v.kind] || ""}
                alt=""
                className="h-12 w-20 shrink-0 rounded-lg"
                imgClassName="object-cover"
              />
              <div className="min-w-0 flex-1 sm:w-48 sm:flex-none">
                <p className="text-sm font-medium text-fg">{labels[v.kind]}</p>
                <p className="truncate text-xs text-fg-muted">{pick?.item.title}</p>
              </div>
              <div className="order-last flex w-full items-center gap-3 sm:order-none sm:w-auto sm:flex-1">
                <input
                  type="range"
                  min={0}
                  max={100}
                  value={v.share}
                  disabled={!v.enabled}
                  aria-label={t("{ingredient} share", { ingredient: labels[v.kind] || "" })}
                  onChange={(e) => ingredient(i, { share: Number(e.currentTarget.value) })}
                  className="min-w-0 flex-1 accent-primary"
                />
                <span className="w-9 text-right text-xs tabular-nums text-fg-muted">
                  {v.share}%
                </span>
              </div>
              <Switch
                checked={v.enabled}
                aria-label={labels[v.kind]}
                onChange={(e) => ingredient(i, { enabled: e.currentTarget.checked })}
              />
              {v.kind === "collections" && v.enabled ? (
                <MultiSelect
                  ariaLabel={t("Your picks (collections)")}
                  value={(v.collections ?? []).map(String)}
                  onChange={(values) => ingredient(i, { collections: values.map(Number) })}
                  placeholder={t("Choose collections…")}
                  searchPlaceholder={t("Search…")}
                  noMatchesLabel={t("No matches")}
                  clearLabel={t("Clear")}
                  selectAllLabel={t("Select all")}
                  options={collections
                    .filter((c) => c.ratingKey && Number.isFinite(Number(c.ratingKey)))
                    .map((c) => ({ value: c.ratingKey!, label: c.name }))}
                />
              ) : null}
            </div>
          );
        })}
      </div>
      <div className="divide-y divide-border">
        <SettingsRow
          title={
            <span className="flex items-center gap-2">
              {t("Pause when they leave")}
              <InfoTip
                text={t(
                  "The channel pauses while this viewer is away and resumes when they return.",
                )}
              />
            </span>
          }
          control={
            <Switch
              checked={recipe.pauseWhenAway}
              aria-label={t("Pause when they leave")}
              onChange={(e) => set({ pauseWhenAway: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          title={t("Channel name")}
          control={
            <Select
              aria-label={t("Channel name")}
              value={recipe.nameFormat}
              onChange={(e) => set({ nameFormat: e.currentTarget.value })}
            >
              {["{viewer} TV", "{viewer} Live", "Made for {viewer}"].map((value) => (
                <option key={value} value={value}>
                  {t(value)}
                </option>
              ))}
            </Select>
          }
        />
      </div>
      <MoreOptions label={t("More options")}>
        <div className="grid gap-4 p-4 sm:grid-cols-3">
          <Field label={t("Don't repeat within (hours)")}>
            <NumberInput
              min={1}
              max={168}
              value={recipe.repeatHours}
              onChange={(repeatHours) => set({ repeatHours })}
            />
          </Field>
          <Field label={t("Max share per show (%)")}>
            <NumberInput
              min={1}
              max={100}
              value={recipe.maxShowShare}
              onChange={(maxShowShare) => set({ maxShowShare })}
            />
          </Field>
          <Field label={t("Night runtime limit (minutes)")}>
            <NumberInput
              min={0}
              max={240}
              value={recipe.nightMaxMinutes}
              onChange={(nightMaxMinutes) => set({ nightMaxMinutes })}
            />
          </Field>
        </div>
      </MoreOptions>
    </div>
  );
}
