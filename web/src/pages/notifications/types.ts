import { t } from "../../i18n";
import { newId } from "../../lib/api";
import { textOf, type Descriptor, type ExtendText } from "../../lib/extend";

/** Mirrors internal/extend.SettingsOption. */
interface SettingsOption {
  value: string;
  label: ExtendText;
}

/** Mirrors internal/extend.SettingsField (SPEC.md §5.5). */
export interface SettingsField {
  key: string;
  kind: "text" | "secret" | "url" | "select" | "number" | "boolean" | "hidden";
  label: ExtendText;
  hint?: ExtendText;
  placeholder?: string;
  required?: boolean;
  secret?: boolean;
  wide?: boolean;
  default?: string;
  min?: number;
  max?: number;
  options?: SettingsOption[];
}

/** Mirrors internal/extend.ChannelKindMeta. */
export interface ChannelKindMeta {
  fields?: SettingsField[];
  sendTest?: boolean;
  selectable?: boolean;
  formats?: string[];
  maxLength?: number;
}

/** Mirrors internal/extend.EventKindMeta. */
interface EventKindMeta {
  automatic?: boolean;
  critical?: boolean;
  channelKinds?: string[];
}

type ChannelKindDescriptor = Descriptor<ChannelKindMeta>;
type EventKindDescriptor = Descriptor<EventKindMeta>;

export type Channel = {
  id: string;
  name: string;
  kind: string;
  enabled: boolean;
  events: string[];
  settings?: Record<string, string>;
  secrets?: Record<string, string>;
  perMinute: number;
  digestMinutes: number;
};

export type Delivery = {
  id: number;
  channel: string;
  created: number;
  status: string;
  error?: string;
  kind?: string;
  attempted?: number;
  due?: number;
};

export type NotificationsData = {
  channels: Channel[];
  deliveries: Delivery[];
  crashWatcher?: boolean;
};

export type NewsletterSettings = {
  enabled: boolean;
  weekday: number;
  hour: number;
  userId: number;
  nextDue: number;
  lastAttempt: number;
  lastStatus: string;
};

export function channelKindDescriptors(
  descriptors: readonly Descriptor[] = [],
): ChannelKindDescriptor[] {
  return descriptors.filter((d): d is ChannelKindDescriptor => d.kind === "channelKind");
}

export function eventKindDescriptors(
  descriptors: readonly Descriptor[] = [],
): EventKindDescriptor[] {
  return descriptors.filter((d): d is EventKindDescriptor => d.kind === "eventKind");
}

export function channelKindDescriptor(
  descriptors: readonly Descriptor[] = [],
  kind: string,
): ChannelKindDescriptor | undefined {
  return channelKindDescriptors(descriptors).find((d) => d.id === kind);
}

export function eventKindDescriptor(
  descriptors: readonly Descriptor[] = [],
  kind: string,
): EventKindDescriptor | undefined {
  return eventKindDescriptors(descriptors).find((d) => d.id === kind);
}

export function transportLabel(kind: string, descriptors: readonly Descriptor[] = []): string {
  const d = channelKindDescriptor(descriptors, kind);
  return d ? textOf(d.label) : kind;
}

export function eventLabel(kind: string, descriptors: readonly Descriptor[] = []): string {
  if (kind === "*") return t("All events");
  if (kind === "test") return t("Test notification");
  const d = eventKindDescriptor(descriptors, kind);
  return d ? textOf(d.label) : kind;
}

export function channelName(c: Channel, descriptors: readonly Descriptor[] = []): string {
  return c.name || transportLabel(c.kind, descriptors);
}

export function criticalEventIds(descriptors: readonly Descriptor[] = []): string[] {
  return eventKindDescriptors(descriptors)
    .filter((d) => d.meta?.critical)
    .map((d) => d.id);
}

export function eventAllowedForKind(
  ev: EventKindDescriptor | undefined,
  channelKind: string,
): boolean {
  return !ev?.meta?.channelKinds?.length || ev.meta.channelKinds.includes(channelKind);
}

export function defaultSettingsForKind(
  descriptors: readonly Descriptor[],
  kind: string,
  current?: Record<string, string>,
  secrets?: Record<string, string>,
): Record<string, string> {
  const next: Record<string, string> = { ...current };
  const fields = channelKindDescriptor(descriptors, kind)?.meta?.fields ?? [];
  for (const f of fields) {
    if (!f.secret && f.default && !next[f.key]) {
      next[f.key] = f.key === "smtpMode" && secrets?.endpoint?.endsWith(":465") ? "tls" : f.default;
    }
  }
  return next;
}

export const blankChannel = (
  descriptors: readonly Descriptor[] = [],
  kind = "webhook",
): Channel => {
  const settings = defaultSettingsForKind(descriptors, kind);
  return {
    id: newId("notify"),
    name: "",
    kind,
    enabled: true,
    events: criticalEventIds(descriptors),
    ...(Object.keys(settings).length ? { settings } : {}),
    perMinute: 5,
    digestMinutes: 0,
  };
};
