import { test, expect, vi } from "vitest";
import { referenceLooks } from "./fixtures";
import { MOMENTS } from "./model";
function context() {
  const calls: unknown[] = [];
  const canvas = { width: 320, height: 180 };
  const gradient = { addColorStop: () => {} };
  const ctx = new Proxy(
    { canvas },
    {
      get(target, key) {
        if (key in target) return target[key as keyof typeof target];
        if (key === "measureText") return (s: string) => ({ width: s.length * 20 });
        if (key === "createImageData")
          return (w: number, h: number) => ({ data: new Uint8ClampedArray(w * h * 4) });
        if (key === "createLinearGradient" || key === "createRadialGradient") return () => gradient;
        if (key === "createPattern") return () => "pattern";
        return (...args: unknown[]) => {
          calls.push([key, ...args]);
        };
      },
      set(target, key, value) {
        calls.push([key, value]);
        Reflect.set(target, key, value);
        return true;
      },
    },
  ) as unknown as CanvasRenderingContext2D;
  return { ctx, calls };
}
test("same look, bindings and time produce identical drawing commands", async () => {
  const create = document.createElement.bind(document);
  vi.spyOn(document, "createElement").mockImplementation(((name: string) =>
    name === "canvas"
      ? { getContext: () => context().ctx }
      : create(name)) as typeof document.createElement);
  const { renderLook } = await import("./proto-renderer");
  const bindings = { next: { title: "Arrival" }, viewer: { name: "Alex" } };
  renderLook(context().ctx, referenceLooks[0]!, "ident", bindings, 3);
  for (const look of referenceLooks)
    for (const moment of MOMENTS) {
      const a = context(),
        b = context();
      renderLook(a.ctx, look, moment, bindings, 3);
      renderLook(b.ctx, look, moment, bindings, 3);
      expect(JSON.stringify(a.calls)).toEqual(JSON.stringify(b.calls));
    }
  vi.restoreAllMocks();
});
