import { useState, useEffect } from "react";
import { Button, Input, Select, confirmDialog } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import {
  ROLES,
  studio,
  type Style,
  type StyleScene,
  type Template,
  type Role,
} from "../studio-model";
import type { SceneBindings } from "../scene-renderer";
import { Drawer } from "../../../../components/Drawer";
import { DescribeLook } from "../DescribeLook";
import { ScenePoster } from "../Stage";
export function SceneStrip(p: {
  style: Style;
  selected: string;
  bindings: SceneBindings;
  onSelect(id: string): void;
  onChange(style: Style): void;
}) {
  const [ai, setAI] = useState("");
  const [templates, setTemplates] = useState<Template[]>([]);
  useEffect(() => {
    void studio.templates().then(setTemplates);
  }, []);
  const add = (role: Role, source?: StyleScene) => {
    const id = crypto.randomUUID();
    const scene: StyleScene = source
      ? { ...structuredClone(source), id, role }
      : {
          id,
          role,
          name: t("Untitled scene"),
          enabled: true,
          weight: 1,
          sounds: [],
          document: {
            v: 1,
            moment: role,
            duration: p.style.roles[role]?.duration ?? 8,
            poster: 3,
            layers: [],
          },
        };
    scene.document.duration = p.style.roles[role]?.duration ?? 8;
    p.onChange({ ...p.style, scenes: [...p.style.scenes, scene] });
    p.onSelect(id);
    return id;
  };
  const change = (scene: StyleScene) =>
    p.onChange({ ...p.style, scenes: p.style.scenes.map((s) => (s.id === scene.id ? scene : s)) });
  return (
    <div className="space-y-4">
      {ai && (
        <Drawer open stacked size="lg" title={t("Describe a scene")} onClose={() => setAI("")}>
          <DescribeLook look={p.style} sceneId={ai} onChange={p.onChange} />
        </Drawer>
      )}
      {ROLES.map((role) => (
        <section key={role} className="space-y-2">
          <div className="flex flex-wrap items-center gap-3">
            <h3 className="text-xs font-semibold uppercase">{t(role)}</h3>
            <label className="flex items-center gap-2 text-xs">
              {t("Duration")}
              <input
                type="range"
                min="4"
                max="28"
                step="4"
                value={p.style.roles[role]?.duration ?? 8}
                aria-label={t("Role duration")}
                onChange={(e) => {
                  const duration = Number(e.target.value);
                  p.onChange({
                    ...p.style,
                    roles: { ...p.style.roles, [role]: { duration } },
                    scenes: p.style.scenes.map((s) =>
                      s.role === role ? { ...s, document: { ...s.document, duration } } : s,
                    ),
                  });
                }}
              />
              {p.style.roles[role]?.duration ?? 8}
            </label>
          </div>
          <div className="flex gap-3 overflow-x-auto pb-2">
            {p.style.scenes
              .filter((s) => s.role === role)
              .map((scene) => (
                <div
                  key={scene.id}
                  className={`w-44 shrink-0 space-y-1 rounded-lg border p-2 ${scene.id === p.selected ? "border-primary" : "border-border"}`}
                >
                  <button
                    className="block w-full"
                    onClick={() => p.onSelect(scene.id)}
                    aria-label={scene.name}
                  >
                    <ScenePoster scene={scene} bindings={p.bindings} />
                  </button>
                  <Input
                    aria-label={t("Scene name")}
                    value={scene.name}
                    onChange={(e) => change({ ...scene, name: e.target.value })}
                  />
                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      aria-label={t("Enabled")}
                      checked={scene.enabled}
                      onChange={(e) => change({ ...scene, enabled: e.target.checked })}
                    />
                    <Input
                      type="number"
                      min="1"
                      max="10"
                      aria-label={t("Rotation weight")}
                      value={scene.weight}
                      onChange={(e) =>
                        change({
                          ...scene,
                          weight: Math.min(10, Math.max(1, Number(e.target.value))),
                        })
                      }
                    />
                  </div>
                  <div className="flex">
                    <Button size="sm" variant="ghost" onClick={() => add(role, scene)}>
                      {t("Duplicate")}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={async () => {
                        if (
                          await confirmDialog({
                            title: t("Delete scene?"),
                            message: t("This scene will be removed from the style."),
                            confirmLabel: t("Delete"),
                            tone: "danger",
                          })
                        )
                          p.onChange({
                            ...p.style,
                            scenes: p.style.scenes.filter((s) => s.id !== scene.id),
                          });
                      }}
                    >
                      {t("Delete")}
                    </Button>
                  </div>
                </div>
              ))}
            <div className="flex w-44 shrink-0 flex-col justify-center gap-2 rounded-lg border border-dashed border-border p-3">
              <Button size="sm" variant="ghost" onClick={() => add(role)}>
                {t("Add {role} scene", { role: t(role) })}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setAI(add(role))}>
                {t("Add with AI")}
              </Button>
              <Select
                aria-label={t("Copy template scene")}
                value=""
                onChange={(e) => {
                  const [ti, si] = e.target.value.split(":").map(Number);
                  const source = templates[ti!]?.style.scenes[si!];
                  if (source) add(role, source);
                }}
              >
                <option value="" disabled>
                  {t("From template…")}
                </option>
                {templates.flatMap((template, ti) =>
                  template.style.scenes.flatMap((scene, si) =>
                    scene.role === role ? (
                      <option key={`${ti}:${si}`} value={`${ti}:${si}`}>
                        {template.name} · {scene.name}
                      </option>
                    ) : (
                      []
                    ),
                  ),
                )}
              </Select>
            </div>
          </div>
        </section>
      ))}
    </div>
  );
}
