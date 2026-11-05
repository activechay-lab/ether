import { AgentPanel } from "@braedonsaunders/appkit-ai/react";
import { Alert, AlertDescription, Button, Textarea } from "@braedonsaunders/appkit-ui";
import { Check } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { t } from "../../../i18n";
import { apiStream, errorMessage } from "../../../lib/api";
import {
  agentPanelLabels,
  cancelAgentTurn,
  fetchAgentStatus,
} from "../../../components/agent/agentApi";
import { readAgentStream } from "../../plugins/LiveStage";
import { onair, type Look } from "./model";

export function DescribeLook({ look, onChange }: { look: Look; onChange: (l: Look) => void }) {
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const [steps, setSteps] = useState<{ label: string; before: Look }[]>([]);
  const current = useRef(look),
    change = useRef(onChange);
  current.current = look;
  change.current = onChange;
  const thread = useRef<string | undefined>(undefined),
    turn = useRef<string | null>(null);
  useEffect(() => {
    let live = true;
    void fetchAgentStatus()
      .then((s) => {
        if (live) setEnabled(s.enabled);
      })
      .catch(() => {
        if (live) setEnabled(false);
      });
    return () => {
      live = false;
    };
  }, []);
  const send = async (prompt: string, signal: AbortSignal): Promise<Response> => {
    setError("");
    const res = await apiStream("/agent/turn", {
      method: "POST",
      signal,
      body: JSON.stringify({
        threadId: thread.current,
        prompt,
        context: {
          route: "/livetv/look",
          record: { type: "look", id: current.current.id },
          draft: { look: current.current, channelId: current.current.channelIds[0] },
        },
      }),
    });
    const id = res.headers.get("X-Ether-Thread-Id");
    if (id) thread.current = id;
    if (!res.ok || !res.body) return res;
    setWorking(true);
    const stop = () => {
      if (turn.current) void cancelAgentTurn(turn.current).catch(() => {});
    };
    signal.addEventListener("abort", stop, { once: true });
    const [mine, panel] = res.body.tee();
    const calls = new Map<string, string>();
    void readAgentStream(new Response(mine), (c) => {
      if (c.type === "start" && c.messageId) turn.current = c.messageId;
      if (c.type === "tool-input-available" && c.toolCallId && c.toolName)
        calls.set(c.toolCallId, c.toolName);
      if (
        c.type === "tool-output-available" &&
        c.toolCallId &&
        calls.get(c.toolCallId) === "look_draft"
      ) {
        const draft = c.output?.look as Look | undefined;
        if (draft?.brand && draft.moments) {
          const before = structuredClone(current.current);
          const next = { ...draft, id: before.id, channelIds: before.channelIds };
          setSteps((s) => [
            ...s,
            {
              before,
              label: t("Accent → {accent} · Type → {display} / {body}", {
                accent: next.brand.palette.accent,
                display: next.brand.type.display,
                body: next.brand.type.body,
              }),
            },
          ]);
          current.current = next;
          change.current(next);
        }
      }
      if (c.type === "error" && c.errorText) setError(c.errorText);
    })
      .catch((e) => {
        if (!signal.aborted) setError(errorMessage(e));
      })
      .finally(() => {
        signal.removeEventListener("abort", stop);
        turn.current = null;
        setWorking(false);
      });
    return new Response(panel, { status: res.status, headers: res.headers });
  };
  return (
    <div className="flex min-h-[570px] flex-col gap-4">
      {!enabled ? (
        <Alert>
          <AlertDescription>
            {t("No model connected. Use a template from your description.")}{" "}
            <Link className="text-primary underline" to="/services/overview#llm">
              {t("Integrations › Language model")}
            </Link>
          </AlertDescription>
        </Alert>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : null}
      {steps.length ? (
        <div className="space-y-2 rounded-lg border border-border bg-bg-subtle p-3">
          {steps.map((step, i) => (
            <button
              key={i}
              className="flex w-full items-center gap-2 text-left text-xs text-fg-muted hover:text-primary"
              title={t("Revert change")}
              disabled={working}
              onClick={() => {
                change.current(step.before);
                setSteps((s) => s.slice(0, i));
              }}
            >
              <Check size={13} className="text-success" />
              {step.label}
            </button>
          ))}
        </div>
      ) : null}
      <form
        className={enabled ? "order-2 space-y-3" : "space-y-3"}
        onSubmit={async (e) => {
          e.preventDefault();
          setWorking(true);
          setError("");
          try {
            const next = await onair.template(description, current.current);
            setSteps((s) => [
              ...s,
              { label: t("Template applied"), before: structuredClone(current.current) },
            ]);
            change.current(next);
          } catch (e) {
            setError(errorMessage(e));
          } finally {
            setWorking(false);
          }
        }}
      >
        <label className="block text-sm font-medium" htmlFor="style-description">
          {t("Describe your style")}
        </label>
        <Textarea
          id="style-description"
          value={description}
          maxLength={2000}
          onChange={(e) => setDescription(e.currentTarget.value)}
          placeholder={t("A cosy late-night movie channel")}
        />
        <Button type="submit" disabled={working || !description.trim()}>
          {t("Use description template")}
        </Button>
        <p className="text-xs text-fg-muted">
          {t("Matches mood keywords to a complete built-in style. No AI required.")}
        </p>
      </form>
      {enabled ? (
        <AgentPanel
          enabled={enabled}
          working={working}
          labels={{
            ...agentPanelLabels(),
            title: t("Build with Ether"),
            welcomeTitle: t("What does your channel feel like?"),
            welcomeDescription: t("Describe the colours, mood and motion."),
            disabledTitle: t("Connect a model"),
          }}
          suggestions={[
            t("A late-night style for {name}", { name: look.name }),
            t("Match the style to this channel’s programming"),
            t("Make it feel like a 90s kids network"),
            t("Give it a calmer motion"),
          ]}
          send={send}
        />
      ) : null}
    </div>
  );
}
