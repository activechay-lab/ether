import { uuid } from "../lib/api";
import { t } from "../i18n";
import { ROLES } from "../pages/livetv/onair/studio-model";
import type { Style } from "../pages/livetv/onair/studio-model";

/** Offline agent output uses the same cumulative draft protocol as production. */
export function createStyleSteps(base: Style, prompt: string, sceneId?: string) {
  const look = structuredClone(base);
  const kids = /kids|cosy|cozy|sunday/i.test(prompt);
  const noir = /noir|thriller|night/i.test(prompt);
  if (!sceneId) {
    look.brand.palette = kids
      ? { background: "#25204d", accent: "#ffb85c", text: "#fff8ea", glow: "#a3edce" }
      : noir
        ? { background: "#080d18", accent: "#d8b26e", text: "#f4eedf", glow: "#657d9b" }
        : { background: "#12002b", accent: "#ff4fd8", text: "#ffffff", glow: "#22e6ff" };
    look.brand.type = {
      display: kids ? "fredoka" : noir ? "playfairdisplay" : "orbitron",
      body: "inter",
    };
    look.brand.energy = kids ? 0.35 : noir ? 0.22 : 0.8;
    look.brand.textures = noir ? ["grain", "vignette"] : kids ? ["grain"] : ["scanlines", "grain"];
    look.brand.tagline = kids
      ? t("A little wonder, all day")
      : noir
        ? t("Stay up for the story")
        : t("Turn up the night");
  }
  const scenes = look.scenes.sort((a, b) => ROLES.indexOf(a.role) - ROLES.indexOf(b.role));
  const cumulative = sceneId ? scenes.filter((s) => s.id !== sceneId) : [];
  const step = (text: string) => ({
    text,
    draft: {
      toolCallId: uuid(),
      toolName: "look_draft",
      input: { look: structuredClone({ ...look, scenes: cumulative }) },
      output: { look: structuredClone({ ...look, scenes: cumulative }) },
    },
  });
  const steps = [step(t("I’m finding the colours, type and mood for your channel. "))];
  for (const scene of scenes.filter((s) => !sceneId || s.id === sceneId)) {
    scene.document.layers = scene.document.layers.filter(
      (layer) => layer.id !== `create-accent-${scene.id}`,
    );
    scene.document.layers.push({
      id: `create-accent-${scene.id}`,
      name: t("Accent line"),
      type: "rect",
      x: 140,
      y: 880,
      w: 1640,
      h: 6,
      fill: "{accent}",
      opacity: {
        keys: [
          [0, 0, "linear"],
          [1, 1, "out"],
        ],
      },
    });
    cumulative.push(scene);
    steps.push(step(t("Now I’m bringing {name} to life. ", { name: scene.name })));
  }
  return [
    ...steps,
    { text: t("Your style is ready to try. Keep this updates the draft; Save makes it yours.") },
  ];
}
