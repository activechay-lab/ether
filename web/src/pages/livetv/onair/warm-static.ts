// The Signal WarmGenerator phosphor model, sampled by the scene preview.
// The small field is intentional analogue bandwidth; Canvas samples it smoothly
// into the native stage. No tile, clip phase, or coarse greyscale snow is used.
function makeCanvas() {
  const c =
    typeof OffscreenCanvas === "undefined"
      ? document.createElement("canvas")
      : new OffscreenCanvas(320, 180);
  c.width = 320;
  c.height = 180;
  return c;
}
const fields = new Map<
  number,
  { tick: number; state: number; ema: Float32Array; canvas: OffscreenCanvas | HTMLCanvasElement }
>();
function tracking(seed: number, tick: number) {
  const mask = (1n << 64n) - 1n;
  let x = (BigInt(seed) ^ (BigInt(Math.floor(tick / 6)) * 0x9e3779b97f4a7c15n)) & mask;
  x = ((x ^ (x >> 30n)) * 0xbf58476d1ce4e5b9n) & mask;
  x = ((x ^ (x >> 27n)) * 0x94d049bb133111ebn) & mask;
  x ^= x >> 31n;
  return x % 397n === 0n
    ? { line: Number((x >> 16n) % 180n), gain: 2 + Number((x >> 32n) % 3n) }
    : undefined;
}
export function warmStatic(seed: number, tick: number): OffscreenCanvas | HTMLCanvasElement {
  let f = fields.get(seed);
  if (!f || tick < f.tick) {
    f = {
      tick: -1,
      state: seed | 1,
      ema: new Float32Array(160 * 90),
      canvas: makeCanvas(),
    };
    if (fields.size >= 8) fields.delete(fields.keys().next().value!);
    fields.set(seed, f);
  }
  const ema = f.ema,
    temp = new Float32Array(ema.length),
    blur = new Float32Array(ema.length);
  for (let n = f.tick + 1; n <= tick; n++) {
    if (n % 2) continue;
    for (let i = 0; i < ema.length; i++) {
      let state = f.state;
      state ^= state << 13;
      state ^= state >>> 17;
      state ^= state << 5;
      f.state = state;
      ema[i] = Math.fround(
        ema[i]! + Math.fround(0.035 * Math.fround(state / 2147483648 - ema[i]!)),
      );
    }
  }
  f.tick = tick;
  for (let y = 0; y < 90; y++)
    for (let x = 0; x < 160; x++) {
      const i = y * 160 + x;
      temp[i] =
        (ema[y * 160 + Math.max(0, x - 1)]! + 2 * ema[i]! + ema[y * 160 + Math.min(159, x + 1)]!) *
        0.25;
    }
  for (let y = 0; y < 90; y++)
    for (let x = 0; x < 160; x++) {
      const i = y * 160 + x;
      blur[i] =
        (temp[Math.max(0, y - 1) * 160 + x]! +
          2 * temp[i]! +
          temp[Math.min(89, y + 1) * 160 + x]!) *
        0.25;
    }
  const tear = tracking(seed, tick);
  const t = tick / 30,
    amplitude = 68 * (1 + 0.12 * Math.sin(t * 0.23));
  const bar = 180 * (0.5 + 0.33 * Math.sin(t * 0.043) + 0.06 * Math.sin(t * 0.017 * Math.SQRT2));
  const context = f.canvas.getContext("2d")! as
      CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D,
    pixels = context.createImageData(320, 180);
  for (let y = 0; y < 180; y++)
    for (let x = 0; x < 320; x++) {
      const sx = x >> 1,
        sy = y >> 1,
        dx = (x % 2) * 0.5,
        dy = (y % 2) * 0.5;
      const a = blur[sy * 160 + sx]! * (1 - dx) + blur[sy * 160 + Math.min(159, sx + 1)]! * dx;
      const b =
        blur[Math.min(89, sy + 1) * 160 + sx]! * (1 - dx) +
        blur[Math.min(89, sy + 1) * 160 + Math.min(159, sx + 1)]! * dx;
      const nx = (x - 160) / 160,
        ny = (y - 90) / 90,
        gain = 0.4 + 0.6 * Math.pow(Math.max(0, 1 - nx * nx * 0.56 - ny * ny * 0.48), 0.7);
      const line = 0.7 * Math.sin((y * Math.PI) / 2) + 0.35 * Math.sin(t * 0.31 + y * 0.11);
      let luma = Math.max(
        24,
        Math.min(
          205,
          Math.floor(
            (139 + (a * (1 - dy) + b * dy) * amplitude) * gain -
              1.8 * Math.exp(-Math.pow((y - bar) / (180 * 0.13), 2)) +
              line +
              0.5,
          ),
        ),
      );
      if (tear?.line === y) luma = Math.min(205, luma + tear.gain);
      const cx = (Math.floor(x / 2) * 2 - 160) / 160,
        cy = (Math.floor(y / 2) * 2 - 90) / 90;
      const cg = 0.4 + 0.6 * Math.pow(Math.max(0, 1 - cx * cx * 0.56 - cy * cy * 0.48), 0.7);
      const u = Math.floor(128 - 8 * cg) - 128,
        v = Math.floor(128 + 9 * cg) - 128,
        yy = luma - 16,
        i = (y * 320 + x) * 4;
      pixels.data[i] = (298 * yy + 459 * v + 128) >> 8;
      pixels.data[i + 1] = (298 * yy - 55 * u - 136 * v + 128) >> 8;
      pixels.data[i + 2] = (298 * yy + 541 * u + 128) >> 8;
      pixels.data[i + 3] = 255;
    }
  context.putImageData(pixels, 0, 0);
  return f.canvas;
}
