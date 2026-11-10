import { Button, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { artUrl, librariesApi, type KindItem, type Library, type MatchRecord } from "./api";

function fieldText(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.map(fieldText).filter(Boolean).join(", ");
  if (typeof v === "object") {
    const o = v as Record<string, unknown>;
    return fieldText(o.name ?? o.title ?? "");
  }
  return String(v);
}

/**
 * Plugin item drawer (spec §11): declared fields, children, artwork,
 * the match panel (pin/clear/re-match, §7) and the last resolution.
 */
export function PluginItemDrawer({
  library,
  itemRef,
  onClose,
  onOpenItem,
}: {
  library: Library;
  itemRef: string;
  onClose: () => void;
  onOpenItem: (ref: string) => void;
}) {
  const detail = useAsync(
    (signal) => librariesApi.item(library.id, itemRef, signal),
    [library.id, itemRef],
  );
  const [busy, setBusy] = useState(false);
  const data = detail.data;
  const item: KindItem | undefined = data?.item;
  const kind = data?.kind ?? library.kind;
  const matches: MatchRecord[] = data?.matches ?? [];
  const manual = matches.find((m) => m.source === "manual" && m.chosen);

  const matchAction = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    try {
      await fn();
      await detail.reload();
      toast.success(done);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer open onClose={onClose} title={item?.title ?? t("Item")} size="lg">
      {detail.loading && !data ? <LoadingRows /> : null}
      {detail.error ? (
        <ErrorPanel error={detail.error} onRetry={() => void detail.reload()} />
      ) : null}
      {item ? (
        <div className="flex flex-col gap-5">
          <div className="plugin-item-head">
            {artUrl(item) ? (
              <img className="plugin-item-art" src={artUrl(item)} alt="" loading="lazy" />
            ) : null}
            <div>
              <p className="text-sm text-fg-muted">
                {kind?.levels.find((l) => l.key === item.level)?.label ?? item.level}
                {item.year ? ` · ${item.year}` : ""}
                {item.playable ? ` · ${t("Playable")}` : ""}
              </p>
              {item.summary ? <p className="mt-1 text-sm">{item.summary}</p> : null}
            </div>
          </div>

          {kind?.fields?.length ? (
            <section aria-label={t("Details")}>
              <h3>{t("Details")}</h3>
              <dl className="plugin-item-fields">
                {kind.fields.map((f) => {
                  const text = fieldText(item.fields?.[f.key]);
                  if (!text) return null;
                  return (
                    <div key={f.key}>
                      <dt>{f.label}</dt>
                      <dd>{text}</dd>
                    </div>
                  );
                })}
              </dl>
            </section>
          ) : null}

          {data && data.children.length ? (
            <section aria-label={t("Inside")}>
              <h3>{t("Inside")}</h3>
              <ul className="plugin-item-children">
                {data.children.map((child) => (
                  <li key={child.ref}>
                    <button type="button" onClick={() => onOpenItem(child.ref)}>
                      {child.title}
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}

          {item.art?.length ? (
            <section aria-label={t("Artwork")}>
              <h3>{t("Artwork")}</h3>
              <ul className="plugin-item-children">
                {item.art.map((a) => (
                  <li key={a.role}>
                    <span>
                      {a.role}
                      {a.width ? ` · ${a.width}×${a.height ?? 0}` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}

          <section aria-label={t("Matching")}>
            <h3>{t("Matching")}</h3>
            <p className="text-sm text-fg-muted">
              {manual
                ? t("You picked which file this item opens. Ether uses it every time.")
                : t(
                    "Ether picks the best-matching file automatically. Pick one yourself to keep it.",
                  )}
            </p>
            {matches.length ? (
              <ul className="plugin-match-list">
                {matches.map((m) => (
                  <li key={m.candidateRef} className={m.chosen ? "is-chosen" : ""}>
                    <div className="min-w-0 flex-1">
                      <p className="plugin-match-ref">{m.candidateRef}</p>
                      <p className="text-xs text-fg-muted">
                        {t("{score}% match", { score: Math.round(m.score * 100) })}
                        {m.source === "manual" ? ` · ${t("Chosen by you")}` : ""}
                        {m.reasons?.length ? ` · ${m.reasons.join("; ")}` : ""}
                      </p>
                    </div>
                    {m.chosen && m.source === "manual" ? (
                      <span className="text-xs">{t("In use")}</span>
                    ) : (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy}
                        onClick={() =>
                          void matchAction(
                            () => librariesApi.pinMatch(library.id, itemRef, m.candidateRef),
                            t("Match saved"),
                          )
                        }
                      >
                        {t("Use this")}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-fg-muted">{t("No matches yet.")}</p>
            )}
            <div className="mt-3 flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() =>
                  void matchAction(
                    () => librariesApi.rematch(library.id, itemRef),
                    t("Matches refreshed"),
                  )
                }
              >
                {t("Match again")}
              </Button>
              {manual ? (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy}
                  onClick={() =>
                    void matchAction(
                      () => librariesApi.clearMatch(library.id, itemRef),
                      t("Your pick was cleared"),
                    )
                  }
                >
                  {t("Clear my pick")}
                </Button>
              ) : null}
            </div>
          </section>

          {data?.resolution ? (
            <section aria-label={t("Last played from")}>
              <h3>{t("Last played from")}</h3>
              <p className="plugin-match-ref">
                {fieldText(data.resolution.name) ||
                  fieldText(data.resolution.pattern) ||
                  t("A saved source")}
              </p>
            </section>
          ) : null}
        </div>
      ) : null}
    </Drawer>
  );
}
