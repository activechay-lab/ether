import { Badge, Button, toast } from "@braedonsaunders/appkit-ui";
import { Check, LayoutTemplate, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { t, tn } from "../../i18n";
import { api, errorMessage } from "../../lib/api";
import type {
  CollectionTemplateBundle,
  CollectionTemplateBundlePreview,
} from "../../lib/contract";

/**
 * Starter collection bundles card shown on the Setup Wizard's Catalogue step.
 * Lets owners seed curated rows (Core Defaults, Streaming Originals, Awards,
 * Seasonal, Franchises) with one click or inspect a dry-run preview first.
 */
export function StarterBundlesCard() {
  const [bundles, setBundles] = useState<CollectionTemplateBundle[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [applied, setApplied] = useState<Record<string, number>>({});
  const [preview, setPreview] = useState<CollectionTemplateBundlePreview | null>(null);

  useEffect(() => {
    void api
      .templateBundles()
      .then(setBundles)
      .catch(() => {});
  }, []);

  if (!bundles.length) return null;

  const togglePreview = async (bundleId: string) => {
    if (busy) return;
    if (preview?.bundle.id === bundleId) {
      setPreview(null);
      return;
    }
    setBusy(bundleId);
    try {
      const res = await api.previewTemplateBundle(bundleId);
      setPreview(res);
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  };

  const apply = async (bundleId: string) => {
    if (busy) return;
    setBusy(bundleId);
    try {
      const res = await api.applyTemplateBundle(bundleId);
      setApplied((prev) => ({
        ...prev,
        [bundleId]: (prev[bundleId] ?? 0) + res.createdCount,
      }));
      if (preview?.bundle.id === bundleId) {
        const nextPreview = await api.previewTemplateBundle(bundleId);
        setPreview(nextPreview);
      }
      toast.success(t("Template bundle applied"), {
        description: tn(
          res.createdCount,
          "Created {count} collection ({skipped} already present).",
          "Created {count} collections ({skipped} already present).",
          { skipped: res.skippedCount },
        ),
      });
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  };

  return (
    <section
      className="space-y-3 rounded-xl border border-border bg-surface p-4"
      aria-labelledby="setup-starter-bundles"
    >
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 id="setup-starter-bundles" className="text-sm font-semibold text-fg">
            {t("Starter collection bundles")}
          </h3>
          <p className="text-sm text-fg-muted">
            {t(
              "Optional one-click collection packs to populate your Home and library rows right away.",
            )}
          </p>
        </div>
        <LayoutTemplate size={18} className="shrink-0 text-primary" aria-hidden />
      </div>
      <div className="grid gap-2.5 sm:grid-cols-2">
        {bundles.map((b) => {
          const isPreviewing = preview?.bundle.id === b.id;
          const isDone = applied[b.id] !== undefined;
          return (
            <div
              key={b.id}
              className="flex flex-col justify-between rounded-lg border border-border bg-bg-subtle/50 p-3"
            >
              <div>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-semibold text-fg">{b.name}</span>
                  <Badge variant={isDone ? "success" : "secondary"}>
                    {tn(b.templateIds.length, "{count} template", "{count} templates")}
                  </Badge>
                </div>
                <p className="mt-1 text-xs text-fg-muted">{b.description}</p>
              </div>
              <div className="mt-3 flex flex-wrap items-center gap-1.5">
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 px-2.5 text-xs"
                  disabled={busy === b.id}
                  onClick={() => void togglePreview(b.id)}
                >
                  {isPreviewing ? t("Hide preview") : t("Preview bundle")}
                </Button>
                <Button
                  size="sm"
                  className="h-7 px-2.5 text-xs"
                  disabled={busy === b.id}
                  onClick={() => void apply(b.id)}
                >
                  {isDone ? <Check size={12} /> : <Plus size={12} />}{" "}
                  {isDone ? t("Applied") : t("Apply bundle")}
                </Button>
              </div>
              {isPreviewing && preview ? (
                <div className="mt-2.5 rounded-md border border-border bg-surface p-2 text-[11px]">
                  <p className="mb-1 font-medium text-fg">
                    {tn(
                      preview.willCreate,
                      "{count} to create ({skipped} already present)",
                      "{count} to create ({skipped} already present)",
                      { skipped: preview.willSkip },
                    )}
                  </p>
                  <ul className="max-h-28 space-y-1 overflow-y-auto">
                    {preview.items.map((item) => (
                      <li
                        key={item.templateId}
                        className="flex items-center justify-between gap-2 text-fg-muted"
                      >
                        <span className="truncate">{item.name}</span>
                        <span className="shrink-0">
                          {item.action === "skip_existing" ? t("Added") : t("Will create")}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}
