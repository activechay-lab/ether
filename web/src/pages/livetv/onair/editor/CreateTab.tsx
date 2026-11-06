import "./create-reveal.css";
import { AgentPanel } from "@braedonsaunders/appkit-ai/react";
import { Button } from "@braedonsaunders/appkit-ui";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  agentPanelLabels,
  fetchAgentStatus,
  cancelAgentTurn,
} from "../../../../components/agent/agentApi";
import { t } from "../../../../i18n";
import { apiStream, errorMessage } from "../../../../lib/api";
import { readAgentStream } from "../../../plugins/LiveStage";
import { studio, type Style } from "../studio-model";
import { StyleThumbnail } from "./RemixPanel";

export function CreateTab({
  style,
  onChange,
  selectedSceneId,
}: {
  style: Style;
  onChange(style: Style): void;
  selectedSceneId?: string;
}) {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [proposal, setProposal] = useState<Style | null>(null);
  const [before, setBefore] = useState<Style | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [narrative, setNarrative] = useState("");
  const [lastPrompt, setLastPrompt] = useState("");
  const thread = useRef<string | undefined>(undefined);
  const turn = useRef<string | undefined>(undefined);
  const controller = useRef<AbortController | null>(null);
  const chat = useRef<HTMLDivElement>(null);
  const revision = useRef(0);
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
      revision.current++;
      controller.current?.abort();
      if (turn.current) void cancelAgentTurn(turn.current).catch(() => {});
    };
  }, []);
  const fill = (value: string) => {
    const box = chat.current?.querySelector("textarea");
    if (!box) return;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(box, value);
    box.dispatchEvent(new Event("input", { bubbles: true }));
    box.focus();
  };
  const send = async (prompt: string, signal: AbortSignal): Promise<Response> => {
    setError("");
    setBusy(true);
    setLastPrompt(prompt);
    setNarrative("");
    const current = ++revision.current;
    const abort = new AbortController();
    controller.current = abort;
    const stop = () => {
      abort.abort();
      if (turn.current) void cancelAgentTurn(turn.current).catch(() => {});
    };
    signal.addEventListener("abort", stop, { once: true });
    try {
      const response = await apiStream("/agent/turn", {
        method: "POST",
        signal: abort.signal,
        body: JSON.stringify({
          threadId: thread.current,
          prompt,
          context: {
            route: "/livetv/look",
            draft: { look: proposal ?? style, sceneId: selectedSceneId },
          },
        }),
      });
      thread.current = response.headers.get("X-Ether-Thread-Id") ?? thread.current;
      if (!response.ok || !response.body) {
        setBusy(false);
        return response;
      }
      const [preview, panel] = response.body.tee();
      // Serial validation keeps drafts in stream order and prevents stale proposals winning.
      let pending = Promise.resolve();
      void readAgentStream(new Response(preview), (chunk) => {
        if (chunk.type === "start") turn.current = chunk.messageId;
        if (chunk.type === "text-start") setNarrative("");
        if (chunk.type === "text-delta" && chunk.delta) setNarrative((n) => n + chunk.delta);
        if (chunk.type === "error")
          setError(chunk.errorText ?? t("Ether could not finish that. Try again."));
        if (chunk.type === "tool-output-available" && chunk.output?.look) {
          const draft = chunk.output.look as Style;
          pending = pending
            .then(async () => {
              const scenes: Style["scenes"] = [];
              for (const scene of draft.scenes) scenes.push(await studio.validateScene(scene));
              if (current === revision.current && !abort.signal.aborted)
                setProposal(
                  selectedSceneId
                    ? {
                        ...(proposal ?? style),
                        scenes: (proposal ?? style).scenes.map((scene) =>
                          scene.id === selectedSceneId
                            ? (scenes.find((next) => next.id === scene.id) ?? scene)
                            : scene,
                        ),
                      }
                    : { ...draft, id: style.id, channelIds: style.channelIds, scenes },
                );
            })
            .catch((e) => {
              if (current === revision.current) setError(errorMessage(e));
            });
        }
      })
        .catch((e) => {
          if (!abort.signal.aborted) setError(errorMessage(e));
        })
        .finally(async () => {
          await pending;
          if (current === revision.current) setBusy(false);
          turn.current = undefined;
          signal.removeEventListener("abort", stop);
        });
      return new Response(panel, { headers: response.headers });
    } catch (e) {
      setBusy(false);
      signal.removeEventListener("abort", stop);
      throw e;
    }
  };
  const preview = proposal ?? style;
  return (
    <section className="flex min-h-0 flex-col gap-4">
      <div className="space-y-2">
        <h2 className="text-xl font-semibold">{t("Create")}</h2>
        <p className="text-sm text-fg-muted">
          {selectedSceneId ? t("Describe a change") : t("Describe your channel")}
        </p>
      </div>
      {enabled === false ? (
        <div className="rounded-xl border border-border p-6">
          <p>{t("Connect a language model to use Ask Ether.")}</p>
          <Link className="text-primary underline" to="/services">
            {t("Integrations")} → {t("LLM")}
          </Link>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap gap-2">
            {[
              t("80s synthwave movie channel"),
              t("Cosy Sunday kids"),
              t("Noir late-night thrillers"),
            ].map((idea) => (
              <Button key={idea} variant="outline" disabled={busy} onClick={() => fill(idea)}>
                {idea}
              </Button>
            ))}
          </div>
          <div
            className="max-h-[50vh] space-y-4 overflow-y-auto rounded-xl border border-border bg-surface p-4"
            aria-label={t("Live build")}
          >
            <div
              className="flex flex-wrap items-center gap-4"
              style={{
                color: preview.brand.palette.text,
                background: preview.brand.palette.background,
                borderRadius: 12,
                padding: 20,
              }}
            >
              {preview.brand.logo?.length === 64 ? (
                <img
                  className="h-12 w-12 object-contain"
                  src={studio.thumb(preview.brand.logo)}
                  alt={t("Logo")}
                />
              ) : (
                <span className="text-3xl font-bold">{preview.brand.logo}</span>
              )}
              <div className="min-w-0 flex-1">
                <p className="text-2xl" style={{ fontFamily: preview.brand.type.display }}>
                  {preview.name}
                </p>
                <p style={{ fontFamily: preview.brand.type.body }}>{preview.brand.tagline}</p>
              </div>
              <div className="flex gap-1">
                {Object.entries(preview.brand.palette).map(([key, color]) => (
                  <span
                    key={key}
                    title={color}
                    className="h-8 w-8 rounded-full border border-white/30"
                    style={{ background: color }}
                  />
                ))}
              </div>
            </div>
            <p className="text-xs text-fg-muted">
              {t("Energy")} {Math.round(preview.brand.energy * 100)}% · {preview.brand.type.display}{" "}
              / {preview.brand.type.body}
            </p>
            <div className="flex flex-wrap gap-2">
              {preview.brand.textures.map((texture) => (
                <span key={texture} className="rounded-full bg-primary/10 px-3 py-1 text-xs">
                  {t(texture)}
                </span>
              ))}
            </div>
            <p role="status" className="text-sm text-fg-muted">
              {narrative || t("Describe the style below, then watch it come to life.")}
            </p>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {preview.scenes.map((scene) => (
                <div
                  key={scene.id}
                  className="studio-create-scene rounded-lg border border-border p-2"
                >
                  <StyleThumbnail style={preview} scene={scene} playing />
                  <p className="mt-2 text-sm">{scene.name}</p>
                </div>
              ))}
            </div>
          </div>
          {error && (
            <p role="alert" className="text-danger">
              {error}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button
              disabled={!proposal || busy}
              onClick={() => {
                setBefore(structuredClone(style));
                onChange(proposal!);
                setProposal(null);
              }}
            >
              {t("Keep this")}
            </Button>
            <Button variant="outline" disabled={!proposal || busy} onClick={() => fill("")}>
              {t("Refine")}
            </Button>
            <Button
              variant="outline"
              disabled={!lastPrompt || busy}
              onClick={() => {
                fill(lastPrompt);
                requestAnimationFrame(() =>
                  chat.current
                    ?.querySelector("textarea")
                    ?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })),
                );
              }}
            >
              {t("Try again")}
            </Button>
            <Button
              variant="ghost"
              disabled={busy || (!before && !proposal)}
              onClick={() => {
                if (before) onChange(before);
                setBefore(null);
                setProposal(null);
                setNarrative("");
              }}
            >
              {t("Revert")}
            </Button>
          </div>
          <p className="text-xs text-fg-muted">
            {t("Keep this updates your draft. Save the style when you’re ready.")}
          </p>
          <div ref={chat} className="flex min-h-80 flex-col">
            <AgentPanel
              enabled={enabled === true}
              labels={{ ...agentPanelLabels(), placeholder: t("Describe your channel") }}
              toolLabels={{ look_draft: t("Draft a style") }}
              send={send}
            />
          </div>
        </>
      )}
    </section>
  );
}
