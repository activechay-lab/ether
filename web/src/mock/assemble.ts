/** Offline counterpart of personalize.arrangeLayout. Production previews use the Go renderer. */
import type { HomeLayout, HomeRow, LayoutSection } from "../lib/contract";
import { effectiveLength, effectivePolicy, whenMatches, type Effective } from "../lib/layout";

interface AssembleContext {
  viewer: { id: number; title: string } | null;
  section: LayoutSection;
  device: "tv" | "mobile" | "web" | "desktop";
  at: Date;
  defaultRows: number;
}
interface Candidate {
  row: HomeRow;
  eff: Effective | null;
  rank: number;
  explicitAlways: boolean;
}

function weekEligible(user: number, row: string, at: Date, n: number): boolean {
  if (!n || n >= 7) return true;
  let hash = 2166136261;
  for (const byte of new TextEncoder().encode(`${user}:${row}`))
    hash = Math.imul(hash ^ byte, 16777619) >>> 0;
  return (((at.getDay() + 6) % 7) + (hash % 7)) % 7 < n;
}

export function assemble(cands: HomeRow[], doc: HomeLayout, ctx: AssembleContext): HomeRow[] {
  const viewerId = ctx.viewer?.id ?? null;
  const when = { day: ctx.at.getDay(), hour: ctx.at.getHours(), device: ctx.device, date: ctx.at };
  const pool: Candidate[] = [];
  const hidden: HomeRow[] = [];
  for (const input of cands) {
    const eff = effectivePolicy(doc, viewerId, ctx.section, input.hubId, input.kind);
    const row: HomeRow = {
      ...input,
      hidden: false,
      hiddenCode: undefined,
      hiddenReason: undefined,
      slot: undefined,
      anchorSlot: undefined,
      pinned: false,
      layer: "personalized",
      placement: "Chosen by personalization",
      policy: eff?.policy,
      policyLayer: eff?.layer,
    };
    const p = eff?.policy;
    let explicitAlways = false;
    if (p && eff) {
      const code =
        p.status === "off"
          ? eff.layer === "override"
            ? "off_override"
            : "off_global"
          : !whenMatches(p.when, when)
            ? "outside_schedule"
            : !weekEligible(viewerId ?? 0, p.row, ctx.at, p.maxDaysPerWeek ?? 0)
              ? "weekly_cap"
              : "";
      const reason =
        code === "off_override"
          ? "Off in override layer"
          : code === "off_global"
            ? "Off in global layer"
            : code === "outside_schedule"
              ? "Outside schedule"
              : code === "weekly_cap"
                ? "Weekly frequency cap"
                : "";
      if (eff.layer === "override") {
        row.layer = "override";
        row.placement = "Viewer override";
      }
      if (reason) {
        row.layer = eff.layer;
        row.hidden = true;
        row.hiddenCode = code;
        row.hiddenReason = reason;
        hidden.push(row);
        continue;
      }
      if (p.status === "always" || p.slot) {
        explicitAlways = true;
        row.layer = eff.layer;
        row.placement = `Always included by ${eff.layer}`;
      }
      if (p.slot) {
        row.anchorSlot = p.slot;
        row.pinned = true;
        row.layer = eff.layer;
        row.placement = `${eff.layer} anchor at slot ${p.slot}`;
      }
    }
    pool.push({ row, eff, rank: 0, explicitAlways });
  }
  for (const [i, c] of pool.entries()) {
    const p = c.eff?.policy;
    c.rank = pool.length - i;
    if (p?.priority) c.rank *= p.priority;
    if (c.explicitAlways) c.rank += 10000;
    if (p?.slot && c.eff?.layer === "override") c.rank += 100000;
  }
  pool.sort((a, b) => b.rank - a.rank);

  const length = effectiveLength(doc, viewerId, ctx.section, ctx.device, ctx.defaultRows).n;
  const used = new Set<Candidate>();
  const counts = new Map<string, number>();
  const visible: HomeRow[] = [];

  let remainingAlways = 0;
  let maxTargetSlot = length;
  for (const c of pool) {
    if (c.explicitAlways) {
      remainingAlways++;
      const p = c.eff?.policy;
      if ((p?.slot ?? 0) > maxTargetSlot) maxTargetSlot = p!.slot!;
      if ((p?.minSlot ?? 0) > maxTargetSlot) maxTargetSlot = p!.minSlot!;
    }
  }
  const slotLimit = Math.max(maxTargetSlot, pool.length + remainingAlways);
  for (let slot = 1; slot <= slotLimit; slot++) {
    if (visible.length >= length && remainingAlways === 0) break;
    const forceAlways = visible.length + remainingAlways >= length;
    let pick: Candidate | undefined;
    let deadline = 101;
    for (const c of pool) {
      if (used.has(c)) continue;
      if (forceAlways && !c.explicitAlways) continue;
      const p = c.eff?.policy;
      if (p) {
        if (!c.explicitAlways && p.maxPerPage && (counts.get(p.row) ?? 0) >= p.maxPerPage) continue;
        if (p.slot) {
          if (p.slot > slot) continue;
          if (p.slot < slot && !c.explicitAlways) continue;
        }
        if ((p.minSlot ?? 0) > slot) continue;
        if (p.maxSlot && p.maxSlot < slot && !c.explicitAlways) continue;
        if (p.slot && (p.slot === slot || (p.slot < slot && c.explicitAlways))) {
          pick = c;
          break;
        }
        if (p.maxSlot && p.maxSlot < deadline) {
          pick = c;
          deadline = p.maxSlot;
          continue;
        }
      }
      pick ??= c;
    }
    if (!pick) {
      if (remainingAlways > 0) continue;
      break;
    }
    used.add(pick);
    if (pick.explicitAlways) remainingAlways--;
    if (pick.eff) counts.set(pick.eff.policy.row, (counts.get(pick.eff.policy.row) ?? 0) + 1);
    visible.push({ ...pick.row, slot: visible.length + 1 });
  }
  for (const c of pool) {
    if (!used.has(c))
      hidden.push({
        ...c.row,
        hidden: true,
        hiddenCode: "page_constraints",
        hiddenReason: "Page length, frequency cap or position constraints",
      });
  }
  return [...visible, ...hidden];
}
