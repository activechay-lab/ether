import { Badge, toast, type ContextMenuEntry } from "@braedonsaunders/appkit-ui";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ActionMenu } from "../../components/ActionMenu";
import { Drawer } from "../../components/Drawer";
import { SlotAnchor } from "../../components/SlotXray";
import { t } from "../../i18n";
import { apiFetch, errorMessage } from "../../lib/api";
import { textOf, useRegistry } from "../../lib/extend";
import { PluginView, ProposalCard, pluginActionPath, pluginProposalPath } from "../PluginView";
import type { PluginActionResult, PluginOpen, PluginProposal } from "../context";

interface SlotMeta {
  slot: string;
  key: string;
  route?: string;
  action?: string;
}
export function useSlots(slot: string) {
  const { descriptors } = useRegistry<SlotMeta>("slot");
  return descriptors.filter((d) => d.owner !== "builtin" && d.meta?.slot === slot);
}
export function PanelSlots({
  slot,
  recordType,
  recordId,
  tmdbId,
  imdbId,
  titleKey,
  type,
}: {
  slot: "title.panel" | "dashboard.panel";
  recordType?: string;
  recordId?: string;
  tmdbId?: number;
  imdbId?: string;
  titleKey?: string;
  type?: string;
}) {
  const slots = useSlots(slot);
  if (!slots.length) return null;
  return (
    <SlotAnchor className="space-y-4">
      {slots.map((d) => {
        const content = (
          <PluginView
            plugin={d.owner}
            page={d.meta!.key}
            params={{ recordType, recordId, tmdbId, imdbId, titleKey, type }}
          />
        );
        const heading = (
          <span className="inline-flex items-center gap-2 font-semibold">
            {textOf(d.label)} <Badge variant="secondary">{d.group || d.owner}</Badge>
          </span>
        );
        return slots.length > 1 && slot === "title.panel" ? (
          <LazyPanel key={d.id} heading={heading}>
            {content}
          </LazyPanel>
        ) : (
          <section key={d.id} className="rounded-xl border border-border bg-surface p-4 space-y-4">
            {heading}
            <VisiblePanel>{content}</VisiblePanel>
          </section>
        );
      })}
    </SlotAnchor>
  );
}
function VisiblePanel({ children }: { children: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    const node = ref.current;
    if (!node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        setVisible(true);
        observer.disconnect();
      }
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  return (
    <div ref={ref} className="min-h-px">
      {visible ? children : null}
    </div>
  );
}
function LazyPanel({ children, heading }: { children: React.ReactNode; heading: React.ReactNode }) {
  const [visible, setVisible] = useState(false);
  return (
    <details
      className="rounded-xl border border-border p-4"
      onToggle={(event) => setVisible(event.currentTarget.open)}
    >
      <summary className="cursor-pointer">{heading}</summary>
      {visible ? <div className="mt-4">{children}</div> : null}
    </details>
  );
}
export function PluginActionMenu({
  slot,
  recordId,
  items = [],
  label,
  onRefresh,
}: {
  slot: `actionMenu.${"title" | "viewer" | "collection" | "release"}`;
  recordId: string;
  items?: ContextMenuEntry[];
  label?: string;
  onRefresh?: () => void;
}) {
  const slots = useSlots(slot);
  const navigate = useNavigate();
  const [proposal, setProposal] = useState<{ plugin: string; value: PluginProposal } | null>(null);
  const [busy, setBusy] = useState(false);
  const [drawer, setDrawer] = useState<{ plugin: string; value: PluginOpen } | null>(null);
  function apply(plugin: string, result: PluginActionResult | null) {
    if (!result) return;
    if (result.toast) {
      const v =
        typeof result.toast === "string" ? { text: result.toast, kind: "success" } : result.toast;
      if (v.kind === "error") toast.error(v.text);
      else if (v.kind === "info") toast.info(v.text);
      else toast.success(v.text);
    }
    if (result.job) return;
    if (result.refresh) onRefresh?.();
    if (result.proposal) setProposal({ plugin, value: result.proposal });
    if (result.open) {
      const v = result.open;
      if (v.drawer) setDrawer({ plugin, value: v });
      else if (v.page) {
        const q = new URLSearchParams(v.params ?? {}).toString();
        navigate(
          `/p/${encodeURIComponent(plugin)}/${encodeURIComponent(v.page)}${q ? `?${q}` : ""}`,
        );
      } else if (v.route?.startsWith("/") && !v.route.startsWith("//")) navigate(v.route);
    }
  }
  async function act(plugin: string, action: string) {
    try {
      apply(
        plugin,
        await apiFetch<PluginActionResult>(pluginActionPath(plugin, action), {
          method: "POST",
          body: JSON.stringify({
            params: { recordType: slot.slice("actionMenu.".length), recordId },
          }),
        }),
      );
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }
  async function decide(decision: "approved" | "rejected") {
    if (!proposal) return;
    setBusy(true);
    try {
      const result = await apiFetch<PluginActionResult>(
        pluginProposalPath(proposal.plugin, proposal.value.id),
        { method: "POST", body: JSON.stringify({ decision }) },
      );
      setProposal(null);
      apply(proposal.plugin, result);
      if (!result?.toast && !result?.refresh) onRefresh?.();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const entries: ContextMenuEntry[] = slots
    .filter((d) => d.meta?.action)
    .map((d) => ({
      key: d.id,
      label: textOf(d.label),
      onSelect: () => void act(d.owner, d.meta!.action!),
    }));
  if (!items.length && !entries.length && !proposal && !drawer) return null;
  return (
    <>
      <SlotAnchor className="inline-block">
        {items.length || entries.length ? (
          <ActionMenu items={[...items, ...entries]} label={label} />
        ) : null}
      </SlotAnchor>
      <Drawer open={!!proposal} onClose={() => setProposal(null)} title={t("Needs your approval")}>
        {proposal ? (
          <ProposalCard proposal={proposal.value} busy={busy} onDecision={(d) => void decide(d)} />
        ) : null}
      </Drawer>
      <Drawer open={!!drawer} onClose={() => setDrawer(null)} title={t("Plugin")}>
        {drawer?.value.drawer ? (
          <PluginView
            plugin={drawer.plugin}
            page={drawer.value.drawer}
            params={drawer.value.params}
          />
        ) : null}
      </Drawer>
    </>
  );
}
