import { test, expect, vi } from "vitest";
import { createCanvas } from "@napi-rs/canvas";
import { referenceLooks } from "./fixtures";
import { MOMENTS } from "./model";
test("same look, bindings and time produce identical pixels for every brand and moment", async () => {
  vi.stubGlobal("document", { createElement: () => createCanvas(256, 256) });
  const { renderLook } = await import("./proto-renderer");
  for (const look of referenceLooks)
    for (const moment of MOMENTS) {
      const a = createCanvas(320, 180),
        b = createCanvas(320, 180);
      const bindings = { next: { title: "Arrival" }, viewer: { name: "Alex" } };
      renderLook(
        a.getContext("2d") as unknown as CanvasRenderingContext2D,
        look,
        moment,
        bindings,
        3,
      );
      renderLook(
        b.getContext("2d") as unknown as CanvasRenderingContext2D,
        look,
        moment,
        bindings,
        3,
      );
      expect(Buffer.from(a.getContext("2d").getImageData(0, 0, 320, 180).data)).toEqual(
        Buffer.from(b.getContext("2d").getImageData(0, 0, 320, 180).data),
      );
    }
  vi.unstubAllGlobals();
});
