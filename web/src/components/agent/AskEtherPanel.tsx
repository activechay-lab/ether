/**
 * The Ask Ether transcript and composer, loaded lazily by AskEther.tsx.
 */
import {
  AgentPanel,
  type AgentApprovalRequestLabels,
  type AgentMessage,
  type AgentPanelLabels,
  type AgentSecretRequestLabels,
} from "@braedonsaunders/appkit-ai/react";
import { useMemo } from "react";
import { t } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { useRegistry } from "../../lib/extend";
import { humanizeToolName, registrySuggestions, toolLabelsFromDescriptors } from "./agentApi";

const KNOWN_TOOLS = [
  "server_status",
  "health_details",
  "playback_history",
  "resolve_trace",
  "release_health",
  "logs_tail",
  "catalog_search",
  "title_details",
  "collection_list",
  "collection_preview",
  "collection_draft",
  "livetv_channels",
  "sources_list",
  "plugins_list",
  "plugin_health",
  "approval_status",
  "navigate",
  "collection_create_from_prompt",
  "release_blocklist",
  "release_pin",
  "format_scores_set",
  "rule_add",
  "source_add_stremio",
  "source_add_m3u",
  "frontend_enable",
  "plugin_enable",
  "server_restart",
];

/** Page defaults used until WS1d serves agentSuggestion command descriptors. */
function defaultSuggestion(route: string): string {
  if (route.startsWith("/releases")) return t("Why did this title pick this release?");
  if (route.startsWith("/livetv")) return t("Which channels are failing?");
  if (route.startsWith("/sources")) return t("Add a plugin from Stremio");
  return t("What needs my attention?");
}

/**
 * The panel plus its registry-driven suggestions and tool titles. It only
 * mounts while the drawer is open (AppKit's Drawer unmounts its body on
 * close), so the top bar never fetches the registry on pages where the
 * owner never opens Ask Ether.
 */
export default function AskEtherPanel({
  route,
  messages,
  labels,
  approvalRequestLabels,
  secretRequestLabels,
  send,
  onDecideApprovalRequest,
  onSubmitSecretRequest,
  hasOlderMessages,
  onLoadOlderMessages,
}: {
  route: string;
  messages: AgentMessage[];
  labels: Partial<AgentPanelLabels>;
  approvalRequestLabels: Partial<AgentApprovalRequestLabels>;
  secretRequestLabels: Partial<AgentSecretRequestLabels>;
  send: (prompt: string, signal: AbortSignal) => Promise<Response>;
  onDecideApprovalRequest: (
    approvalId: string,
    decision: "approved" | "rejected",
    note?: string,
  ) => Promise<void>;
  onSubmitSecretRequest: (requestId: string, value: string) => Promise<void>;
  hasOlderMessages: boolean;
  onLoadOlderMessages: () => Promise<void>;
}) {
  const { locale } = useLocale();
  const { descriptors } = useRegistry("command", "tool");

  const suggestions = useMemo(() => {
    const fromRegistry = registrySuggestions(
      descriptors.filter((d) => d.kind === "command"),
      route,
    );
    const fallback = defaultSuggestion(route);
    const out = [...fromRegistry];
    if (!out.includes(fallback)) out.push(fallback);
    return out.slice(0, 4);
    // locale re-renders the fallback copy in the active language.
  }, [descriptors, route, locale]);

  const toolLabels = useMemo(() => {
    const humanized: Record<string, string> = {};
    for (const name of KNOWN_TOOLS) humanized[name] = t(humanizeToolName(name));
    return {
      ...humanized,
      ...toolLabelsFromDescriptors(descriptors.filter((d) => d.kind === "tool")),
    };
  }, [descriptors, locale]);

  return (
    <AgentPanel
      enabled
      initialMessages={messages}
      suggestions={suggestions}
      labels={labels}
      approvalRequestLabels={approvalRequestLabels}
      secretRequestLabels={secretRequestLabels}
      toolLabels={toolLabels}
      send={send}
      onDecideApprovalRequest={onDecideApprovalRequest}
      onSubmitSecretRequest={onSubmitSecretRequest}
      hasOlderMessages={hasOlderMessages}
      onLoadOlderMessages={onLoadOlderMessages}
    />
  );
}
