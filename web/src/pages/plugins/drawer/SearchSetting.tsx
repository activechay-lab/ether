import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import { useEffect, useRef, useState } from "react";
import { SettingsRow } from "../../../components/SettingsRow";
import { t } from "../../../i18n";
import { useLocale } from "../../../i18n/react";
import { apiFetch, errorMessage } from "../../../lib/api";

type Option = { value: string; label: string };
/** Runtime-backed choices, with explicit search and no provider-specific UI. */
export function SearchSetting({
  plugin,
  field,
  value,
  onChange,
}: {
  plugin: string;
  field: { key: string; label: string; placeholder?: string };
  value: unknown;
  onChange: (value: string) => void;
}) {
  const { locale } = useLocale();
  const [query, setQuery] = useState("");
  const [options, setOptions] = useState<Option[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  useEffect(
    () => () => {
      generation.current++;
    },
    [plugin, field.key, locale],
  );
  // JSON option values may carry structured provider coordinates and a label.
  let label = String(value ?? "");
  try {
    const name: unknown = JSON.parse(label)?.name;
    if (typeof name === "string") label = name;
  } catch {
    /* opaque option */
  }
  const search = async () => {
    const current = ++generation.current;
    setBusy(true);
    setError("");
    try {
      const result = await apiFetch<{ options: Option[] }>(
        `/plugins/${encodeURIComponent(plugin)}/options/${encodeURIComponent(field.key)}?q=${encodeURIComponent(query.trim())}&locale=${encodeURIComponent(locale)}`,
      );
      if (current !== generation.current) return;
      setOptions(result.options);
      if (!result.options.length) setError(t("No results"));
    } catch (e) {
      if (current === generation.current) setError(errorMessage(e));
    } finally {
      if (current === generation.current) setBusy(false);
    }
  };
  return (
    <SettingsRow title={field.label}>
      <div className="space-y-2">
        <form
          className="flex gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            void search();
          }}
        >
          <Input
            aria-label={field.label}
            placeholder={field.placeholder}
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              generation.current++;
              setBusy(false);
              setOptions([]);
            }}
          />
          <Button type="submit" size="sm" disabled={busy || query.trim().length < 2}>
            {busy ? t("Searching…") : t("Search")}
          </Button>
        </form>
        {options.length > 0 ? (
          <Select
            aria-label={t("Search results")}
            value={String(value ?? "")}
            onChange={(event) => onChange(event.target.value)}
          >
            <option value="">{t("Choose a result")}</option>
            {options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </Select>
        ) : null}
        {label ? <p className="text-sm text-fg-muted">{label}</p> : null}
        {error ? (
          <p role="status" className="text-sm text-danger">
            {error}
          </p>
        ) : null}
      </div>
    </SettingsRow>
  );
}
