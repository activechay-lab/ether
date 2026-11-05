import {
  loadOnAirFonts,
  loadSceneAssets,
  renderScene,
  type Scene,
  type SceneBindings,
} from "./scene-renderer";
const fonts = loadOnAirFonts();
let canvas: OffscreenCanvas | undefined;
self.onmessage = async (
  event: MessageEvent<{
    id: number;
    document: Scene;
    bindings: SceneBindings;
    time: number;
    width: number;
    height: number;
  }>,
) => {
  try {
    await fonts;
    const { id, document, bindings, time, width, height } = event.data;
    if (!canvas || canvas.width !== width || canvas.height !== height)
      canvas = new OffscreenCanvas(width, height);
    renderScene(
      canvas.getContext("2d", { willReadFrequently: true })!,
      document,
      await loadSceneAssets(document, bindings),
      time,
    );
    const bitmap = canvas.transferToImageBitmap();
    self.postMessage({ id, bitmap }, { transfer: [bitmap] });
  } catch (error) {
    self.postMessage({ id: event.data.id, error: String(error) });
  }
};
