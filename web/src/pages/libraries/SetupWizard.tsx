import { Button, toast } from "@braedonsaunders/appkit-ui";
import { CheckCircle2, CircleAlert, CircleDashed, SkipForward } from "lucide-react";
import { useEffect, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type PluginSetup, type SetupStep } from "./api";

interface SchemaField {
  key: string;
  label?: string;
  type?: string;
  secret?: boolean;
  options?: { value: string; label: string }[];
  help?: string;
}

/** Flatten a settings schema (groups of fields) into key → field. */
function schemaFields(schema: unknown): Map<string, SchemaField> {
  const out = new Map<string, SchemaField>();
  const walk = (v: unknown) => {
    if (Array.isArray(v)) {
      v.forEach(walk);
    } else if (v && typeof v === "object") {
      const o = v as Record<string, unknown>;
      if (typeof o.key === "string") out.set(o.key, o as unknown as SchemaField);
      Object.values(o).forEach(walk);
    }
  };
  walk(schema);
  return out;
}

function FieldInput({
  field,
  value,
  error,
  onChange,
}: {
  field: SchemaField;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  const label = field.label || field.key;
  const common = { "aria-label": label };
  let input;
  if (field.type === "boolean" || field.type === "switch") {
    input = (
      <input
        type="checkbox"
        checked={Boolean(value)}
        onChange={(e) => onChange(e.target.checked)}
        {...common}
      />
    );
  } else if (field.options?.length) {
    input = (
      <select value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} {...common}>
        <option value="" />
        {field.options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    );
  } else if (field.type === "number" || field.type === "integer") {
    input = (
      <input
        type="number"
        value={value === undefined || value === null ? "" : Number(value)}
        onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))}
        {...common}
      />
    );
  } else {
    input = (
      <input
        type={field.secret || field.type === "password" ? "password" : "text"}
        value={value === undefined || value === null ? "" : String(value)}
        onChange={(e) => onChange(e.target.value)}
        {...common}
      />
    );
  }
  return (
    <label className="library-field">
      <span>{label}</span>
      {input}
      {field.help ? <small>{field.help}</small> : null}
      {error ? (
        <small role="alert" className="library-field-error">
          {error}
        </small>
      ) : null}
    </label>
  );
}

function StepIcon({ step }: { step: SetupStep }) {
  if (step.status === "ok") return <CheckCircle2 size={16} aria-hidden />;
  if (step.status === "failed") return <CircleAlert size={16} aria-hidden />;
  if (step.status === "skipped") return <SkipForward size={16} aria-hidden />;
  return <CircleDashed size={16} aria-hidden />;
}

/**
 * Guided setup drawer (spec §8): walks the plugin's declared steps,
 * saving each step's settings through the sealed plugin settings path
 * and running its test. Required steps block the plugin's libraries
 * until they pass or the owner skips them.
 */
export function SetupWizard({ pluginId, onClose }: { pluginId: string; onClose: () => void }) {
  const setup = useAsync((signal) => librariesApi.setup(pluginId, signal), [pluginId]);
  return (
    <Drawer open onClose={onClose} title={t("Plugin setup")} size="lg">
      {setup.loading && !setup.data ? <LoadingRows /> : null}
      {setup.error ? <ErrorPanel error={setup.error} onRetry={() => void setup.reload()} /> : null}
      {setup.data ? (
        <SetupSteps data={setup.data} reload={() => void setup.reload()} onClose={onClose} />
      ) : null}
    </Drawer>
  );
}

function SetupSteps({
  data,
  reload,
  onClose,
}: {
  data: PluginSetup;
  reload: () => void;
  onClose: () => void;
}) {
  const fields = schemaFields(data.schema);
  const firstOpen = data.steps.findIndex((s) => s.status !== "ok" && s.status !== "skipped");
  const [active, setActive] = useState(firstOpen === -1 ? 0 : firstOpen);
  const [values, setValues] = useState<Record<string, unknown>>(data.settings);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [detail, setDetail] = useState("");
  useEffect(() => setValues(data.settings), [data.settings]);
  const step: SetupStep | undefined = data.steps[active];

  if (!data.steps.length) {
    return <p className="text-sm text-fg-muted">{t("This plugin needs no setup.")}</p>;
  }

  const saveAndTest = async () => {
    if (!step) return;
    setBusy(true);
    setDetail("");
    try {
      const picked: Record<string, unknown> = {};
      for (const k of step.fields) if (k in values) picked[k] = values[k];
      const saved = await librariesApi.saveSetupSettings(data.pluginId, {
        ...data.settings,
        ...picked,
      });
      if (saved.fieldErrors && Object.keys(saved.fieldErrors).length) {
        setFieldErrors(saved.fieldErrors);
        return;
      }
      setFieldErrors({});
      const res = await librariesApi.testSetupStep(data.pluginId, step.key);
      if (res.fields) setFieldErrors(res.fields);
      setDetail(res.detail || "");
      if (res.ok) {
        toast.success(t("That worked"));
        reload();
        if (active < data.steps.length - 1) setActive(active + 1);
      } else {
        toast.error(res.detail || t("That did not work yet"));
        reload();
      }
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const skip = async () => {
    if (!step) return;
    setBusy(true);
    try {
      await librariesApi.skipSetupStep(data.pluginId, step.key);
      reload();
      if (active < data.steps.length - 1) setActive(active + 1);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-fg-muted">
        {data.blockers.length
          ? t("Finish these steps and {name} can start. Its libraries stay hidden until then.", {
              name: data.pluginName,
            })
          : t("{name} is set up. You can re-run any step here.", { name: data.pluginName })}
      </p>
      <ol className="setup-steps">
        {data.steps.map((s, i) => (
          <li key={s.key}>
            <button
              type="button"
              className={i === active ? "setup-step is-active" : "setup-step"}
              aria-current={i === active ? "step" : undefined}
              onClick={() => setActive(i)}
            >
              <StepIcon step={s} />
              <span>{s.label}</span>
              {s.required ? <small>{t("Required")}</small> : null}
            </button>
          </li>
        ))}
      </ol>
      {step ? (
        <section aria-label={step.label}>
          <h3>{step.label}</h3>
          {step.hint ? <p className="text-sm text-fg-muted">{step.hint}</p> : null}
          <div className="mt-3 flex flex-col gap-3">
            {step.fields.map((key) => {
              const field = fields.get(key) ?? { key };
              return (
                <FieldInput
                  key={key}
                  field={field}
                  value={values[key]}
                  error={fieldErrors[key]}
                  onChange={(v) => setValues((prev) => ({ ...prev, [key]: v }))}
                />
              );
            })}
            {step.fields.length === 0 ? (
              <p className="text-sm text-fg-muted">{t("Nothing to fill in for this step.")}</p>
            ) : null}
          </div>
          {detail || step.detail ? (
            <p role="status" className="mt-3 text-sm">
              {detail || step.detail}
            </p>
          ) : null}
          <div className="mt-4 flex flex-wrap gap-2">
            <Button disabled={busy} onClick={() => void saveAndTest()}>
              {step.test ? t("Save and test") : t("Save and continue")}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => void skip()}>
              {t("Skip for now")}
            </Button>
            {active === data.steps.length - 1 && data.blockers.length === 0 ? (
              <Button variant="outline" onClick={onClose}>
                {t("Done")}
              </Button>
            ) : null}
          </div>
        </section>
      ) : null}
    </div>
  );
}
