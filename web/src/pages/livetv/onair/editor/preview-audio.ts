import { useEffect, useRef } from "react";
import { apiBlob } from "../../../../lib/api";
import type { StyleScene } from "../studio-model";
export function usePreviewAudio(
  scene: StyleScene | undefined,
  time: number,
  playing: boolean,
  muted: boolean,
): void {
  const state = useRef<{
    context: AudioContext;
    buffers: Map<string, AudioBuffer>;
    sources: AudioBufferSourceNode[];
    last: number;
    wall: number;
    signature: string;
  } | null>(null);
  useEffect(
    () => () => {
      state.current?.sources.forEach((s) => s.stop());
      void state.current?.context.close();
      state.current = null;
    },
    [],
  );
  useEffect(() => {
    if (!state.current && playing && !muted)
      state.current = {
        context: new AudioContext(),
        buffers: new Map(),
        sources: [],
        last: time,
        wall: performance.now(),
        signature: "",
      };
    const s = state.current;
    if (!s) return;
    const stop = () => {
      s.sources.forEach((source) => {
        try {
          source.stop();
        } catch {
          /* already ended */
        }
      });
      s.sources = [];
    };
    const signature = JSON.stringify(scene?.sounds);
    const elapsed = (performance.now() - s.wall) / 1000,
      delta = time - s.last;
    const rate =
      elapsed > 0.005 && delta > 0 ? (Math.abs(delta / elapsed - 0.5) < 0.2 ? 0.5 : 1) : 1;
    const seek = Math.abs(delta - elapsed * rate) > 0.12;
    s.wall = performance.now();
    s.last = time;
    if (!playing || muted || !scene) {
      stop();
      s.signature = "";
      return;
    }
    if (s.sources.length && !seek && signature === s.signature) return;
    stop();
    s.signature = signature;
    let cancelled = false;
    void s.context.resume();
    void Promise.all(
      scene.sounds.map(async (sound) => {
        let buffer = s.buffers.get(sound.assetDigest);
        if (!buffer) {
          const blob = await apiBlob(`/ether/api/onair/assets/${sound.assetDigest}`);
          buffer = await s.context.decodeAudioData(await blob.arrayBuffer());
          s.buffers.set(sound.assetDigest, buffer);
        }
        if (cancelled) return;
        const length = Math.max(
            0,
            Math.min(buffer.duration, sound.end ?? buffer.duration) - sound.start,
          ),
          local = Math.max(0, time - sound.at);
        if (local >= length) return;
        const source = s.context.createBufferSource(),
          gain = s.context.createGain();
        source.buffer = buffer;
        source.playbackRate.value = rate;
        source.connect(gain);
        gain.connect(s.context.destination);
        const when = s.context.currentTime + Math.max(0, sound.at - time) / rate;
        const envelope = (at: number) =>
          sound.volume *
          Math.min(
            1,
            sound.fadeIn > 0 ? at / sound.fadeIn : 1,
            sound.fadeOut > 0 ? (length - at) / sound.fadeOut : 1,
          );
        gain.gain.setValueAtTime(Math.max(0, envelope(local)), when);
        const points = [sound.fadeIn, length - sound.fadeOut, length]
          .filter((p) => p > local && p <= length)
          .sort((a, b) => a - b);
        points.forEach((p) =>
          gain.gain.linearRampToValueAtTime(Math.max(0, envelope(p)), when + (p - local) / rate),
        );
        source.start(when, sound.start + local, length - local);
        s.sources.push(source);
      }),
    ).catch(() => {
      s.signature = "";
    });
    return () => {
      cancelled = true;
    };
  }, [scene, time, playing, muted]);
}
