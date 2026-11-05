import type { Layer, Scene } from "../scene-renderer";
export type EditorLayer = Layer & {
  name?: string;
  hidden?: boolean;
  locked?: boolean;
  start?: number;
  end?: number;
};
export function findLayer(scene: Scene, id: string): EditorLayer | undefined {
  for (const layer of scene.layers) {
    if (layer.id === id) return layer;
    const child = findLayer({ ...scene, layers: layer.layers ?? [] }, id);
    if (child) return child;
  }
}
export function updateLayer(scene: Scene, id: string, patch: Partial<EditorLayer>): Scene {
  return {
    ...scene,
    layers: scene.layers.map((l) =>
      l.id === id
        ? { ...l, ...patch, id: l.id }
        : l.layers
          ? { ...l, layers: updateLayer({ ...scene, layers: l.layers }, id, patch).layers }
          : l,
    ),
  };
}
export function newLayerId(scene: Scene, base = "layer"): string {
  let i = 1;
  while (findLayer(scene, `${base}-${i}`)) i++;
  return `${base}-${i}`;
}
export function insertLayer(scene: Scene, layer: Layer, parentId?: string, index?: number): Scene {
  const ids = new Set<string>();
  const validate = (l: Layer) => {
    if (ids.has(l.id) || findLayer(scene, l.id)) throw new Error("Duplicate layer id");
    ids.add(l.id);
    l.layers?.forEach(validate);
  };
  validate(layer);
  const add = (ls: Layer[]) => {
    const out = [...ls];
    out.splice(index ?? out.length, 0, layer);
    return out;
  };
  if (!parentId) return { ...scene, layers: add(scene.layers) };
  const parent = findLayer(scene, parentId);
  if (!parent || parent.type !== "group") throw new Error("Parent must be a group");
  return updateLayer(scene, parentId, { layers: add(parent.layers ?? []) });
}
export function removeLayers(scene: Scene, ids: string[]): Scene {
  return {
    ...scene,
    layers: scene.layers
      .filter((l) => !ids.includes(l.id))
      .map((l) =>
        l.layers ? { ...l, layers: removeLayers({ ...scene, layers: l.layers }, ids).layers } : l,
      ),
  };
}
export function moveLayer(
  scene: Scene,
  id: string,
  parentId: string | undefined,
  index: number,
): Scene {
  const layer = findLayer(scene, id);
  if (!layer) return scene;
  if (
    parentId === id ||
    (parentId && findLayer({ ...scene, layers: layer.layers ?? [] }, parentId))
  )
    return scene;
  return insertLayer(removeLayers(scene, [id]), layer, parentId, index);
}
function roots(scene: Scene, ids: string[]): Layer[] {
  const out: Layer[] = [];
  const walk = (ls: Layer[]) =>
    ls.forEach((l) => {
      if (ids.includes(l.id)) out.push(l);
      else if (l.layers) walk(l.layers);
    });
  walk(scene.layers);
  return out;
}
export function groupLayers(scene: Scene, ids: string[]): Scene {
  const selected = roots(scene, ids);
  if (!selected.length) return scene;
  // A group is inserted at the first selected root's paint position. Coordinates remain local.
  let parent: string | undefined;
  let index = scene.layers.length;
  const locate = (ls: Layer[], p?: string): boolean => {
    for (let i = 0; i < ls.length; i++) {
      const l = ls[i]!;
      if (l.id === selected[0]!.id) {
        parent = p;
        index = i;
        return true;
      }
      if (l.layers && locate(l.layers, l.id)) return true;
    }
    return false;
  };
  locate(scene.layers);
  const siblings = parent ? findLayer(scene, parent)!.layers! : scene.layers;
  if (!selected.every((l) => siblings.some((s) => s.id === l.id))) return scene;
  return insertLayer(
    removeLayers(
      scene,
      selected.map((l) => l.id),
    ),
    { id: newLayerId(scene, "group"), type: "group", layers: selected },
    parent,
    index,
  );
}
export function ungroup(scene: Scene, id: string): Scene {
  const walk = (ls: Layer[]): Layer[] =>
    ls.flatMap((l) =>
      l.id === id && l.type === "group"
        ? (l.layers ?? []).map((child) => ({
            ...child,
            x: (Number(child.x) || 0) + (Number(l.x) || 0),
            y: (Number(child.y) || 0) + (Number(l.y) || 0),
          }))
        : [l.layers ? { ...l, layers: walk(l.layers) } : l],
    );
  return { ...scene, layers: walk(scene.layers) };
}
export function duplicateLayers(scene: Scene, ids: string[]): Scene {
  const used = new Set<string>();
  const gather = (ls: Layer[]) =>
    ls.forEach((l) => {
      used.add(l.id);
      if (l.layers) gather(l.layers);
    });
  gather(scene.layers);
  const clone = (l: Layer): Layer => {
    let n = 1;
    while (used.has(`${l.id}-${n}`)) n++;
    const id = `${l.id}-${n}`;
    used.add(id);
    return { ...structuredClone(l), id, layers: l.layers?.map(clone) };
  };
  const selected = new Set(roots(scene, ids).map((l) => l.id));
  const walk = (ls: Layer[]): Layer[] =>
    ls.flatMap((l) =>
      selected.has(l.id) ? [l, clone(l)] : [l.layers ? { ...l, layers: walk(l.layers) } : l],
    );
  return { ...scene, layers: walk(scene.layers) };
}
