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
import { ROLES, studio, type Style } from "../studio-model";
import { StyleThumbnail } from "./RemixPanel";

export function CreateTab({
  style,
  onChange,
  selectedSceneId: sceneId,
  sceneOnly = false,
}: {
  style: Style;
  onChange(style: Style): void;
  selectedSceneId?: string;
  sceneOnly?: boolean;
}) {
  const selectedSceneId = sceneOnly ? sceneId : undefined;
  const [proposal, setProposal] = useState<Style | null>(null);
  const [revealed, setRevealed] = useState<number | null>(null);
  const [reducedMotion, setReducedMotion] = useState(false);
  useEffect(() => {
    const media = matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReducedMotion(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    if (!proposal || revealed === null || revealed >= proposal.scenes.length) return;
    const timer = setTimeout(() => setRevealed((n) => (n ?? 0) + 1), reducedMotion ? 0 : 700);
    return () => clearTimeout(timer);
  }, [proposal, revealed, reducedMotion]);
  const [enabled, setEnabled] = useState<boolean | null>(null);
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
    setRevealed(0);
    setProposal(null);
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
  const building =
    busy || (proposal !== null && revealed !== null && revealed < proposal.scenes.length);
  const roles = {
    ident: t("Ident"),
    upnext: t("Up next"),
    tonight: t("Tonight"),
    break: t("Break"),
    back: t("Back from break"),
    offair: t("Off air"),
    welcome: t("Welcome"),
    bug: t("Corner logo"),
    lowerthird: t("Lower third"),
  };
  const textures: Record<string, string> = {
    grain: t("Grain"),
    flicker: t("Flicker"),
    scanlines: t("Scanlines"),
    vignette: t("Vignette"),
    chroma: t("Chroma"),
  };
  const fontNames: Record<string, string> = {
    inter: "Inter",
    playfairdisplay: "Playfair Display",
    specialelite: "Special Elite",
    fraunces: "Fraunces",
    bungee: "Bungee",
    fredoka: "Fredoka",
    orbitron: "Orbitron",
    vt323: "VT323",
    spacegrotesk: "Space Grotesk",
    ibmplexmono: "IBM Plex Mono",
    archivoblack: "Archivo Black",
    notosans: "Noto Sans",
  };
  const ready = !busy || proposal !== null;
  const scenes = busy
    ? [
        ...preview.scenes,
        ...style.scenes.filter((scene) => !preview.scenes.some((next) => next.id === scene.id)),
      ]
    : preview.scenes;
  return (
    <section className="studio-create">
      <div className="studio-create-conversation">
        <div>
          <p className="text-xs font-medium uppercase tracking-widest text-primary">
            {t("Ask Ether")}
          </p>
          <h2 className="mt-2 text-2xl font-semibold">{t("Describe your channel")}</h2>
        </div>
        {enabled === false ? (
          <div className="rounded-xl border border-border p-6">
            <p>{t("Connect a language model to use Ask Ether.")}</p>
            <Link className="text-primary underline" to="/services">
              {t("Integrations")} → {t("LLM")}
            </Link>
          </div>
        ) : (
          <div ref={chat} className="studio-create-chat">
            <AgentPanel
              enabled={enabled === true}
              labels={{
                ...agentPanelLabels(),
                placeholder: selectedSceneId ? t("Describe a change") : t("Describe your channel"),
              }}
              toolLabels={{ look_draft: t("Draft a style") }}
              send={send}
            />
          </div>
        )}
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
        <p role="status" className="studio-create-narrative text-sm text-fg-muted">
          {narrative || t("Describe the style below, then watch it come to life.")}
        </p>
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
      </div>
      <div className="studio-create-build" aria-label={t("Live build")} aria-busy={building}>
        <div className={`studio-create-kit ${!ready ? "studio-create-pending" : ""}`}>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">{t("Brand kit")}</h3>
            <span className="text-xs text-fg-muted">
              {building ? t("Getting ready for TV") : t("Live build")}
            </span>
          </div>
          <div
            className="studio-create-brand studio-create-arrival"
            key={preview.brand.palette.accent}
            style={{
              background: preview.brand.palette.background,
              color: preview.brand.palette.text,
            }}
          >
            {preview.brand.logo?.length === 64 ? (
              <img
                className="h-12 w-12 object-contain"
                src={studio.thumb(preview.brand.logo)}
                alt={t("Logo")}
              />
            ) : (
              <span
                className="studio-create-monogram"
                style={{ color: preview.brand.palette.accent }}
              >
                {preview.brand.logo || preview.name.slice(0, 2)}
              </span>
            )}
            <div className="min-w-0">
              <p
                className="truncate text-2xl"
                style={{ fontFamily: `onair-${preview.brand.type.display}` }}
              >
                {preview.name}
              </p>
              <p
                className="mt-1 text-sm"
                style={{ fontFamily: `onair-${preview.brand.type.body}` }}
              >
                {preview.brand.tagline}
              </p>
            </div>
          </div>
          <div
            className="studio-create-swatches studio-create-arrival"
            key={JSON.stringify(preview.brand.palette)}
          >
            {Object.entries(preview.brand.palette).map(([key, color]) => (
              <div key={key}>
                <div
                  className="h-10 rounded-lg border border-border"
                  style={{ background: color }}
                />
                <p className="mt-1 text-xs text-fg-muted">{color}</p>
              </div>
            ))}
          </div>
          <div
            className="studio-create-specimens studio-create-arrival"
            key={JSON.stringify(preview.brand.type)}
          >
            {(["display", "body"] as const).map((key) => (
              <div key={key}>
                <p className="text-xs text-fg-muted">
                  {key === "display" ? t("Display font") : t("Body font")}
                </p>
                <p
                  className="mt-1 truncate text-2xl"
                  style={{ fontFamily: `onair-${preview.brand.type[key]}` }}
                >
                  {fontNames[preview.brand.type[key]] ?? t("Library font")}
                </p>
              </div>
            ))}
          </div>
          <div className="flex items-center gap-3">
            <span className="text-xs text-fg-muted">{t("Energy")}</span>
            <meter
              className="studio-create-energy"
              min="0"
              max="1"
              value={preview.brand.energy}
              aria-label={t("Energy")}
            />
            <span className="text-xs">{Math.round(preview.brand.energy * 100)}%</span>
          </div>
          <div className="flex flex-wrap gap-2">
            {preview.brand.textures.map((texture) => (
              <span key={texture} className="rounded-full border border-border px-3 py-1 text-xs">
                {textures[texture] ?? texture}
              </span>
            ))}
          </div>
        </div>
        <div className="studio-create-scenes">
          {scenes.map((scene, index) => (
            <div key={`${revision.current}-${scene.id}`} className="min-w-0">
              {ready &&
              preview.scenes.some((next) => next.id === scene.id) &&
              (revealed === null || index < revealed) ? (
                <div className="studio-create-scene">
                  <StyleThumbnail style={preview} scene={scene} playing={!reducedMotion} />
                </div>
              ) : (
                <div className="studio-create-placeholder">
                  <span>{roles[scene.role]}</span>
                </div>
              )}
              <p className="mt-2 text-xs font-medium text-primary">{roles[scene.role]}</p>
              <p className="truncate text-sm">
                {scene.name === scene.role ? roles[scene.role] : scene.name}
              </p>
            </div>
          ))}
          {scenes.length === 0 &&
            ROLES.slice(0, 6).map((role) => (
              <div key={role} className="studio-create-placeholder">
                {roles[role]}
              </div>
            ))}
        </div>
      </div>
    </section>
  );
}
