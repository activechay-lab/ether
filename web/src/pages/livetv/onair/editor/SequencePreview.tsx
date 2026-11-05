import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import { onair, type Bindings, type Reel } from "../model";
import { studio, type Style } from "../studio-model";
import { StyleThumbnail } from "./RemixPanel";
import { useMediaURL } from "../useMediaURL";
import { usePreviewAudio } from "./preview-audio";
export function SequencePreview({
  style,
  channelId,
  at,
}: {
  style: Style;
  channelId: string;
  at: string;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false),
    [playing, setPlaying] = useState(false),
    [muted, setMuted] = useState(false),
    [time, setTime] = useState(0),
    [bindings, setBindings] = useState<Bindings>({}),
    [reels, setReels] = useState<Reel[]>([]);
  useEffect(() => {
    if (open) dialog.current?.showModal();
  }, [open]);
  useEffect(() => {
    void onair.bindings({ channel: channelId, at }).then(setBindings);
    void onair.reels().then(setReels);
  }, [channelId, at]);
  const chapters = useMemo(() => {
    const scene = (role: string) => style.scenes.find((x) => x.role === role && x.enabled);
    const reel = reels
      .find((x) => x.channelIds.includes(channelId))
      ?.clips.find((c) => c.status === "ready");
    return [
      { label: t("Ident"), scene: scene("ident") },
      { label: t("Programme"), programme: true },
      { label: t("Break"), scene: scene("break") },
      ...(reel
        ? [
            {
              label: reel.name,
              reel: {
                ...reel,
                preview: `/ether/api/onair/reels/${reels.find((r) => r.clips.some((c) => c.id === reel.id))!.id}/clips/${reel.id}/preview`,
              },
            },
          ]
        : []),
      { label: t("Back"), scene: scene("back") },
      { label: t("Up next"), scene: scene("upnext") },
    ]
      .filter((c) => c.scene || "programme" in c || "reel" in c)
      .map((c) => ({
        ...c,
        duration: c.scene
          ? (style.roles[c.scene.role]?.duration ?? 8)
          : "reel" in c
            ? Math.max(1, (c.reel?.durationMs ?? 8000) / 1000)
            : 6,
      }));
  }, [style, reels, channelId]);
  const total = chapters.reduce((n, c) => n + c.duration, 0);
  let start = 0,
    index = 0;
  for (let i = 0; i < chapters.length; i++) {
    index = i;
    if (time < start + chapters[i]!.duration || i === chapters.length - 1) break;
    start += chapters[i]!.duration;
  }
  const chapter = chapters[index],
    local = time - start;
  useEffect(() => {
    if (!open || !playing) return;
    let frame = 0,
      last = performance.now();
    const tick = (now: number) => {
      setTime((v) => {
        const next = v + (now - last) / 1000;
        if (next >= total) {
          setPlaying(false);
          return total;
        }
        return next;
      });
      last = now;
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [open, playing, total]);
  usePreviewAudio(chapter?.scene, local, open && playing, muted);
  const video = useRef<HTMLVideoElement>(null);
  const videoDigest = open
    ? (chapter?.scene?.video?.assetDigest ??
      (chapter && "reel" in chapter ? (chapter.reel?.sourceDigest ?? "") : ""))
    : "";
  const videoSource = useMediaURL(videoDigest ? studio.media(videoDigest) : "");
  useEffect(() => {
    const v = video.current;
    if (!v) return;
    const target = local + (chapter?.scene?.video?.start ?? 0);
    if (Math.abs(v.currentTime - target) > 0.25) v.currentTime = target;
    if (open && playing) void v.play().catch(() => {});
    else v.pause();
  }, [open, playing, local, videoSource]);

  const slots = {
    "channel.name": bindings.channel?.name ?? "",
    "next.title": bindings.next?.title ?? "",
    "next.time": bindings.next?.time ?? "",
    "next.poster": bindings.next?.backdrop ?? "",
    "next.backdrop": bindings.next?.backdrop ?? "",
    "now.title": bindings.now?.title ?? "",
  };
  return (
    <>
      <Button
        onClick={() => {
          setTime(0);
          setOpen(true);
          setPlaying(true);
        }}
      >
        {t("Play the sequence")}
      </Button>
      {open ? (
        <dialog
          ref={dialog}
          onCancel={(e) => {
            e.preventDefault();
            setOpen(false);
            setPlaying(false);
          }}
          role="dialog"
          aria-modal="true"
          aria-label={t("Sequence preview")}
          className="fixed inset-0 z-50 m-0 flex h-dvh max-h-none w-screen max-w-none flex-col border-0 bg-black p-0 text-white"
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              setOpen(false);
              setPlaying(false);
            }
          }}
        >
          <div className="flex justify-between p-3">
            <p>
              {bindings.channel?.name} · {chapter?.label}
            </p>
            <Button
              variant="secondary"
              onClick={() => {
                setOpen(false);
                setPlaying(false);
              }}
            >
              {t("Close")}
            </Button>
          </div>
          <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden">
            {chapter?.scene ? (
              <>
                {chapter.scene.video && videoSource ? (
                  <video
                    ref={video}
                    src={videoSource}
                    muted={muted}
                    playsInline
                    className="absolute inset-0 h-full w-full object-contain"
                  />
                ) : null}
                {chapter.scene.video?.mode !== "replace" ? (
                  <div className="relative w-full">
                    <StyleThumbnail
                      style={style}
                      scene={chapter.scene}
                      slots={slots}
                      time={local}
                    />
                  </div>
                ) : null}
              </>
            ) : chapter && "reel" in chapter ? (
              <video
                key={chapter.reel?.id}
                ref={video}
                src={videoSource || chapter.reel?.preview}
                autoPlay={playing}
                muted={muted}
                controls
                playsInline
                className="h-full w-full object-contain"
              />
            ) : (
              <div className="relative flex h-full w-full items-end bg-neutral-950 p-10">
                {bindings.next?.backdrop ? (
                  <img
                    alt=""
                    src={bindings.next.backdrop}
                    className="absolute inset-0 h-full w-full object-cover"
                  />
                ) : null}
                <div className="relative rounded-lg bg-black/70 p-5">
                  <p className="text-sm">{t("Programme preview")}</p>
                  <p className="text-3xl">{bindings.next?.title}</p>
                </div>
              </div>
            )}
          </div>
          <nav aria-label={t("Sequence chapters")} className="flex gap-2 overflow-x-auto p-3">
            {chapters.map((c, i) => (
              <Button
                key={i}
                variant={index === i ? "secondary" : "outline"}
                aria-current={index === i ? "step" : undefined}
                onClick={() => setTime(chapters.slice(0, i).reduce((n, x) => n + x.duration, 0))}
              >
                {c.label}
              </Button>
            ))}
          </nav>
          <div className="flex gap-2 p-3">
            <Button variant="secondary" onClick={() => setPlaying(!playing)}>
              {playing ? t("Pause") : t("Play")}
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                setTime(0);
                setPlaying(true);
              }}
            >
              {t("Replay")}
            </Button>
            <Button variant="secondary" onClick={() => setMuted(!muted)} aria-pressed={muted}>
              {muted ? t("Unmute") : t("Mute")}
            </Button>
            <input
              aria-label={t("Sequence position")}
              type="range"
              min={0}
              max={total}
              step={0.1}
              value={time}
              className="min-w-0 flex-1"
              onChange={(e) => setTime(+e.target.value)}
            />
          </div>
        </dialog>
      ) : null}
    </>
  );
}
