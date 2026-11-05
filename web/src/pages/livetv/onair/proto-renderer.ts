// @ts-nocheck -- direct TypeScript port of the owner-approved reference engine.
/** PROTOTYPE preview renderer. Delete when the Go renderer ships compiled to WASM (§6.3).
 * Google FontFace fonts are loaded only on On Air screens; self-host these for shipping.
 * All procedural shapes, noise and time inputs are seeded and deterministic.
 */
import type { Look, Moment, Bindings } from "./model";

/* ============================== core =============================== */
const W = 1920,
  H = 1080,
  TAU = Math.PI * 2,
  SC = 1280 / 1920;
const clamp = (x, a = 0, b = 1) => (x < a ? a : x > b ? b : x),
  lerp = (a, b, k) => a + (b - a) * k;
const E = {
  lin: (k) => k,
  inC: (k) => k * k * k,
  outC: (k) => 1 - (1 - k) ** 3,
  outQ: (k) => 1 - (1 - k) ** 5,
  io: (k) => (k < 0.5 ? 4 * k * k * k : 1 - (-2 * k + 2) ** 3 / 2),
  back: (k) => {
    const c1 = 1.70158,
      c3 = c1 + 1;
    return 1 + c3 * (k - 1) ** 3 + c1 * (k - 1) ** 2;
  },
};
const pr = (t, a, b, e = E.lin) => e(clamp((t - a) / (b - a)));
function rnd(s, i) {
  let x = Math.imul((s | 0) ^ Math.imul((i | 0) + 1, 0x9e3779b1), 0x85ebca6b);
  x ^= x >>> 13;
  x = Math.imul(x, 0xc2b2ae35);
  x ^= x >>> 16;
  return (x >>> 0) / 4294967296;
}
function hashStr(s) {
  let h = 2166136261;
  for (const ch of s) {
    h ^= ch.codePointAt(0);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}
function hexA(h, a) {
  const n = parseInt(h.slice(1), 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
}
const LG = (c, x0, y0, x1, y1, st) => {
  const g = c.createLinearGradient(x0, y0, x1, y1);
  st.forEach(([o, col]) => g.addColorStop(o, col));
  return g;
};
const RG = (c, x, y, r0, r1, st) => {
  const g = c.createRadialGradient(x, y, r0, x, y, r1);
  st.forEach(([o, col]) => g.addColorStop(o, col));
  return g;
};
function rr(c, x, y, w, h, r) {
  r = Math.max(0, Math.min(r, w / 2, h / 2));
  c.beginPath();
  c.moveTo(x + r, y);
  c.arcTo(x + w, y, x + w, y + h, r);
  c.arcTo(x + w, y + h, x, y + h, r);
  c.arcTo(x, y + h, x, y, r);
  c.arcTo(x, y, x + w, y, r);
  c.closePath();
}
const FONTS = {
  Bungee: "'Bungee','Arial Black',Impact,sans-serif",
  Fredoka: "'Fredoka','Arial Rounded MT Bold',Arial,sans-serif",
  Elite: "'Special Elite','Courier New',monospace",
  Playfair: "'Playfair Display',Georgia,serif",
  Fraunces: "'Fraunces','Playfair Display',Georgia,serif",
  Inter: "'Inter',system-ui,sans-serif",
  Monoton: "'Monoton','Arial Black',sans-serif",
  Orbitron: "'Orbitron','Arial Black',sans-serif",
  VT: "'VT323','Courier New',monospace",
  Grotesk: "'Space Grotesk','Inter',system-ui,sans-serif",
};
const fnt = (o) =>
  `${o.i ? "italic " : ""}${o.w || 400} ${o.s || 40}px ${FONTS[o.f] || o.f || FONTS.Inter}`;
function meas(c, s, o) {
  c.save();
  c.font = fnt(o);
  const w = c.measureText(s).width + (o.track || 0) * Math.max(0, [...s].length - 1);
  c.restore();
  return w;
}
function wrap(c, s, maxW, o) {
  c.save();
  c.font = fnt(o);
  const out = [];
  let line = "";
  for (const w of s.split(" ")) {
    const tryL = line ? line + " " + w : w;
    if (c.measureText(tryL).width > maxW && line) {
      out.push(line);
      line = w;
    } else line = tryL;
  }
  if (line) out.push(line);
  c.restore();
  return out;
}
/* text: o = {f,s,w,i,a,b,c,alpha,track,sw,sc,glow,gb} ; returns width */
function tokenText(c, s, o) {
  const look = c.__look;
  if (!look) return [s, o];
  const original = {
    kids: "BLORP!",
    lateNight: "NOCTURNE",
    cinema: "MERIDIAN",
    retro: "NEON 84",
    clean: "MERIDIAN",
  }[look.family];
  if (original) s = s.replaceAll(original, look.name.toUpperCase());
  const words = {
    upnext: "Up next",
    tonight: "Tonight",
    break: "We’ll be right back",
    back: "Welcome back",
    offair: "Back soon",
  };
  for (const [key, value] of Object.entries(look.brand.words)) {
    if (value && words[key]) s = s.replace(new RegExp(words[key], "ig"), value);
  }
  if (
    s === "totally gooey TV" ||
    s === "Late night. Lights off." ||
    s === "LATE NIGHT  ·  LIGHTS OFF"
  )
    s = look.brand.tagline;
  const edit = look.layers?.Title;
  if (edit && o.s >= 80) {
    if (edit.text && !edit.text.startsWith("{")) s = edit.text;
    o = {
      ...o,
      s: edit.size || o.s,
      alpha: edit.opacity === undefined ? o.alpha : edit.opacity / 100,
    };
  }
  const display = ["Bungee", "Playfair", "Fraunces", "Monoton", "Orbitron", "Grotesk"].includes(
    o.f,
  );
  return [s, { ...o, f: display ? look.brand.type.display : look.brand.type.body }];
}
function T(c, s, x, y, o = {}) {
  [s, o] = tokenText(c, s, o);
  c.save();
  c.font = fnt(o);
  c.textBaseline = o.b || "alphabetic";
  if (o.alpha != null) c.globalAlpha *= clamp(o.alpha);
  if (c.globalAlpha <= 0.001) {
    c.restore();
    return meas(c, s, o);
  }
  if (o.glow) {
    c.shadowColor = o.glow;
    c.shadowBlur = o.gb || 30;
  }
  const tr = o.track || 0,
    chars = [...s],
    ws = chars.map((ch) => c.measureText(ch).width);
  const total = tr
    ? ws.reduce((a, b) => a + b, 0) + tr * (chars.length - 1)
    : c.measureText(s).width;
  const x0 = x - (o.a === "center" ? total / 2 : o.a === "right" ? total : 0);
  c.textAlign = "left";
  const draw = (str, px) => {
    if (o.sw) {
      c.lineWidth = o.sw;
      c.lineJoin = "round";
      c.strokeStyle = o.sc || "#000";
      c.strokeText(str, px, y);
    }
    if (o.c !== null) {
      c.fillStyle = o.c || "#fff";
      c.fillText(str, px, y);
    }
  };
  if (!tr) draw(s, x0);
  else {
    let px = x0;
    chars.forEach((ch, i) => {
      draw(ch, px);
      px += ws[i] + tr;
    });
  }
  c.restore();
  return total;
}
/* per-character animated text; fn(i,n) -> {dx,dy,sc,sx,sy,rot,al,c} */
function TA(c, s, x, y, o, fn) {
  [s, o] = tokenText(c, s, o);
  c.save();
  c.font = fnt(o);
  const chars = [...s],
    ws = chars.map((ch) => c.measureText(ch).width),
    tr = o.track || 0;
  const total = ws.reduce((a, b) => a + b, 0) + tr * (chars.length - 1);
  let px = x - (o.a === "center" ? total / 2 : o.a === "right" ? total : 0);
  if (!o.a) px = x - total / 2;
  if (o.a === "left") px = x;
  chars.forEach((ch, i) => {
    const k = fn(i, chars.length) || {},
      al = k.al == null ? 1 : k.al,
      sc = k.sc == null ? 1 : k.sc;
    if (al > 0.001 && sc > 0.001) {
      c.save();
      c.translate(px + ws[i] / 2 + (k.dx || 0), y + (k.dy || 0));
      c.rotate(k.rot || 0);
      c.scale(sc * (k.sx || 1), sc * (k.sy || 1));
      c.globalAlpha *= al;
      c.textAlign = "center";
      c.textBaseline = o.b || "middle";
      if (o.glow) {
        c.shadowColor = o.glow;
        c.shadowBlur = o.gb || 30;
      }
      if (o.sh) {
        c.fillStyle = o.sh;
        if (o.sw) {
          c.lineWidth = o.sw;
          c.lineJoin = "round";
          c.strokeStyle = o.sh;
          c.strokeText(ch, o.shx || 8, o.shy || 10);
        }
        c.fillText(ch, o.shx || 8, o.shy || 10);
      }
      if (o.sw) {
        c.lineWidth = o.sw;
        c.lineJoin = "round";
        c.strokeStyle = o.sc || "#000";
        c.strokeText(ch, 0, 0);
      }
      c.fillStyle = k.c || o.c || "#fff";
      c.fillText(ch, 0, 0);
      c.restore();
    }
    px += ws[i] + tr;
  });
  c.restore();
  return total;
}
/* noise + post */
let NOISE = null,
  SCAN = null;
function initTextures() {
  if (NOISE) return;
  NOISE = [0, 1, 2, 3].map((k) => {
    const n = document.createElement("canvas");
    n.width = n.height = 256;
    const x = n.getContext("2d");
    if (!x) return null;
    const im = x.createImageData(256, 256);
    let s = k * 7919 + 1;
    for (let i = 0; i < im.data.length; i += 4) {
      s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
      const v = s >>> 24;
      im.data[i] = im.data[i + 1] = im.data[i + 2] = v;
      im.data[i + 3] = 255;
    }
    x.putImageData(im, 0, 0);
    return n;
  });
  SCAN = (() => {
    const n = document.createElement("canvas");
    n.width = 4;
    n.height = 6;
    const x = n.getContext("2d");
    if (!x) return null;
    x.fillStyle = "rgba(0,0,0,.55)";
    x.fillRect(0, 0, 4, 3);
    return n;
  })();
}
let PATS = null,
  SPAT = null;
function grain(c, t, a, mode = "overlay") {
  initTextures();
  if (!NOISE?.every(Boolean)) return;
  if (!PATS) PATS = NOISE.map((n) => c.createPattern(n, "repeat"));
  const f = Math.floor(t * 24);
  c.save();
  c.globalAlpha = a;
  c.globalCompositeOperation = mode;
  const ox = (f * 97) % 256,
    oy = (f * 61) % 256;
  c.translate(-ox, -oy);
  c.fillStyle = PATS[((f % 4) + 4) % 4];
  c.fillRect(0, 0, W + 256, H + 256);
  c.restore();
}
function scanlines(c, a) {
  initTextures();
  if (!SCAN) return;
  if (!SPAT) SPAT = c.createPattern(SCAN, "repeat");
  c.save();
  c.globalAlpha = a;
  c.fillStyle = SPAT;
  c.fillRect(0, 0, W, H);
  c.restore();
}
function vignette(c, s) {
  c.fillStyle = RG(c, 960, 540, 380, 1250, [
    [0, "rgba(0,0,0,0)"],
    [1, `rgba(0,0,0,${s})`],
  ]);
  c.fillRect(0, 0, W, H);
}
/* shapes */
function blob(c, cx, cy, r, seed, t, amt = 0.08, n = 9, sx = 1, sy = 1) {
  const pts = [];
  for (let i = 0; i < n; i++) {
    const a = (i / n) * TAU;
    const k =
      1 +
      amt * Math.sin(t * 2.1 + rnd(seed, i) * 6.28) +
      amt * 0.6 * Math.sin(t * 3.3 + rnd(seed, i + 50) * 6.28);
    pts.push([cx + Math.cos(a) * r * k * sx, cy + Math.sin(a) * r * k * sy]);
  }
  const mid = (p, q) => [(p[0] + q[0]) / 2, (p[1] + q[1]) / 2];
  c.beginPath();
  let m = mid(pts[n - 1], pts[0]);
  c.moveTo(m[0], m[1]);
  for (let i = 0; i < n; i++) {
    const p = pts[i],
      q = pts[(i + 1) % n],
      m2 = mid(p, q);
    c.quadraticCurveTo(p[0], p[1], m2[0], m2[1]);
  }
  c.closePath();
}
function star4(c, x, y, r, rot, col) {
  c.save();
  c.translate(x, y);
  c.rotate(rot);
  c.fillStyle = col;
  c.beginPath();
  for (let i = 0; i < 8; i++) {
    const a = (i / 8) * TAU - Math.PI / 2,
      rr2 = i % 2 ? r * 0.22 : r;
    c.lineTo(Math.cos(a) * rr2, Math.sin(a) * rr2);
  }
  c.closePath();
  c.fill();
  c.restore();
}
function starBadge(c, x, y, r, fill, ink, n = 16) {
  c.beginPath();
  for (let i = 0; i < n * 2; i++) {
    const a = (i / (n * 2)) * TAU,
      rr2 = i % 2 ? r * 0.86 : r;
    c.lineTo(x + Math.cos(a) * rr2, y + Math.sin(a) * rr2);
  }
  c.closePath();
  c.fillStyle = fill;
  c.fill();
  c.lineWidth = 9;
  c.strokeStyle = ink;
  c.stroke();
}
function heart(c, x, y, s, col) {
  c.fillStyle = col;
  c.beginPath();
  c.moveTo(x, y + s);
  c.bezierCurveTo(x - s * 1.6, y - s * 0.2, x - s * 0.6, y - s * 1.3, x, y - s * 0.4);
  c.bezierCurveTo(x + s * 0.6, y - s * 1.3, x + s * 1.6, y - s * 0.2, x, y + s);
  c.fill();
}
function ridge(c, w, base, amp, seed, col, fr, h) {
  c.beginPath();
  c.moveTo(0, h);
  for (let i = 0; i <= 64; i++) {
    const u = i / 64;
    const n =
      Math.sin(u * TAU * fr * 1.1 + rnd(seed, 1) * 6) * 0.5 +
      Math.sin(u * TAU * fr * 2.9 + rnd(seed, 2) * 6) * 0.3 +
      Math.sin(u * TAU * fr * 6.7 + rnd(seed, 3) * 6) * 0.2;
    c.lineTo(u * w, base - amp * n);
  }
  c.lineTo(w, h);
  c.closePath();
  c.fillStyle = col;
  c.fill();
}
function tree(c, x, y, len, ang, d, seed, s) {
  if (d <= 0) return;
  const x2 = x + Math.cos(ang) * len,
    y2 = y + Math.sin(ang) * len;
  c.lineWidth = d * 1.8 * s;
  c.beginPath();
  c.moveTo(x, y);
  c.lineTo(x2, y2);
  c.stroke();
  tree(c, x2, y2, len * 0.72, ang - 0.35 - rnd(seed, d * 3) * 0.35, d - 1, seed + 1, s);
  tree(c, x2, y2, len * 0.68, ang + 0.3 + rnd(seed, d * 5) * 0.35, d - 1, seed + 2, s);
}
function crescent(c, x, y, r, col) {
  c.save();
  c.beginPath();
  c.rect(0, 0, W, H);
  c.moveTo(x + r * 0.42 + r * 0.86, y - r * 0.22);
  c.arc(x + r * 0.42, y - r * 0.22, r * 0.86, 0, TAU, true);
  c.clip("evenodd");
  c.fillStyle = col;
  c.beginPath();
  c.arc(x, y, r, 0, TAU);
  c.fill();
  c.restore();
}
/* procedural stand-ins for TMDB artwork */
function art(c, kind, seed, x, y, w, h, t = 0) {
  c.save();
  c.beginPath();
  c.rect(x, y, w, h);
  c.clip();
  c.translate(x, y);
  const r = (i) => rnd(seed, i),
    s = h / 1080;
  if (kind === "toon") {
    c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, "#43c6f0"],
      [1, "#c6f3ff"],
    ]);
    c.fillRect(0, 0, w, h);
    const sx = w * 0.8,
      sy = h * 0.2,
      sr = h * 0.09;
    c.save();
    c.translate(sx, sy);
    c.rotate(t * 0.3);
    c.fillStyle = "#ffe14d";
    for (let i = 0; i < 12; i++) {
      c.rotate(TAU / 12);
      c.fillRect(sr * 1.25, -sr * 0.12, sr * 0.6, sr * 0.24);
    }
    c.restore();
    c.fillStyle = "#ffd23f";
    c.beginPath();
    c.arc(sx, sy, sr, 0, TAU);
    c.fill();
    for (let i = 0; i < 3; i++) {
      const cx = ((r(i + 10) * w + t * 30 * (1 + i * 0.3)) % (w * 1.3)) - w * 0.15,
        cy = h * (0.12 + r(i + 20) * 0.25),
        cs = h * (0.05 + r(i + 30) * 0.03);
      c.fillStyle = "#fff";
      [
        [0, 0, 1],
        [1.1, 0.2, 0.8],
        [-1.1, 0.25, 0.75],
        [0.4, -0.5, 0.8],
      ].forEach(([dx, dy, k]) => {
        c.beginPath();
        c.arc(cx + dx * cs, cy + dy * cs, cs * k, 0, TAU);
        c.fill();
      });
    }
    ridge(c, w, h * 0.7, h * 0.07, seed + 1, "#7CE03A", 0.8, h);
    ridge(c, w, h * 0.86, h * 0.06, seed + 2, "#3FB83A", 1.1, h);
    for (let i = 0; i < 30; i++) {
      c.fillStyle = ["#ff3e8a", "#fff", "#ffd23f"][i % 3];
      c.beginPath();
      c.arc(r(i + 40) * w, h * (0.82 + r(i + 70) * 0.16), h * 0.009, 0, TAU);
      c.fill();
    }
  } else if (kind === "night") {
    c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, "#04060d"],
      [0.7, "#171d38"],
      [1, "#261f33"],
    ]);
    c.fillRect(0, 0, w, h);
    for (let i = 0; i < 90; i++) {
      c.fillStyle = `rgba(255,255,255,${(0.25 + 0.6 * r(i + 300)) * (0.6 + 0.4 * Math.sin(t * 2 + i))})`;
      c.fillRect(r(i + 100) * w, r(i + 200) * h * 0.6, 2.5 * s + 1, 2.5 * s + 1);
    }
    const mx = w * 0.74,
      my = h * 0.22,
      mr = h * 0.08;
    c.fillStyle = RG(c, mx, my, mr, mr * 4, [
      [0, "rgba(220,230,255,.25)"],
      [1, "rgba(220,230,255,0)"],
    ]);
    c.fillRect(0, 0, w, h);
    c.fillStyle = "#e9e6da";
    c.beginPath();
    c.arc(mx, my, mr, 0, TAU);
    c.fill();
    ridge(c, w, h * 0.78, h * 0.1, seed + 5, "#090b13", 0.6, h);
    const hx = w * 0.3,
      hy = h * 0.62,
      hs = h * 0.15;
    c.fillStyle = "#06070c";
    c.beginPath();
    c.moveTo(hx - hs * 0.8, hy + hs * 1.2);
    c.lineTo(hx - hs * 0.8, hy);
    c.lineTo(hx, hy - hs * 0.7);
    c.lineTo(hx + hs * 0.8, hy);
    c.lineTo(hx + hs * 0.8, hy + hs * 1.2);
    c.closePath();
    c.fill();
    c.fillRect(hx + hs * 0.35, hy - hs * 0.6, hs * 0.18, hs * 0.4);
    c.fillStyle = `rgba(255,200,90,${0.55 + 0.35 * Math.sin(t * 3 + seed)})`;
    c.fillRect(hx - hs * 0.22, hy + hs * 0.25, hs * 0.2, hs * 0.26);
    c.fillRect(hx + hs * 0.15, hy + hs * 0.25, hs * 0.2, hs * 0.26);
    c.strokeStyle = "#05060a";
    c.lineCap = "round";
    tree(c, w * 0.82, h * 0.95, h * 0.2, -Math.PI / 2, 6, seed, s);
    c.lineCap = "butt";
  } else if (kind === "city") {
    const hue = 200 + r(1) * 60;
    c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, `hsl(${hue},45%,7%)`],
      [0.75, `hsl(${hue + 20},40%,20%)`],
      [1, `hsl(${hue + 40},45%,30%)`],
    ]);
    c.fillRect(0, 0, w, h);
    for (const L of [0, 1]) {
      let bx = -20,
        i = 0;
      while (bx < w) {
        const bw = (60 + r(i * 3 + L * 500) * 120) * s,
          bh = (L ? 0.22 : 0.36) * h + r(i * 3 + 1 + L * 500) * h * (L ? 0.32 : 0.22),
          by = h - bh;
        c.fillStyle = L ? "#05060a" : `hsl(${hue},30%,13%)`;
        c.fillRect(bx, by, bw, bh);
        if (L) {
          c.fillStyle = "rgba(255,214,140,.78)";
          for (let wy = by + 14 * s; wy < h - 12 * s; wy += 24 * s)
            for (let wx = bx + 9 * s; wx < bx + bw - 14 * s; wx += 19 * s) {
              if (rnd(seed + i, ((wx / s) | 0) * 7 + ((wy / s) | 0)) > 0.74)
                c.fillRect(wx, wy, 8 * s, 11 * s);
            }
        }
        bx += bw + 4 * s;
        i++;
      }
    }
    c.fillStyle = LG(c, 0, h * 0.6, 0, h, [
      [0, `hsla(${hue},40%,40%,0)`],
      [1, `hsla(${hue},40%,45%,.35)`],
    ]);
    c.fillRect(0, 0, w, h);
    c.strokeStyle = "rgba(200,215,240,.13)";
    c.lineWidth = 2 * s + 0.5;
    c.beginPath();
    for (let i = 0; i < 70; i++) {
      const rx = (r(i + 600) * w + t * 90) % w,
        ry = (r(i + 700) * h + t * 700) % h;
      c.moveTo(rx, ry);
      c.lineTo(rx - 8 * s, ry + 34 * s);
    }
    c.stroke();
  } else if (kind === "shell") {
    c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, "#8e9a95"],
      [1, "#d8dcd4"],
    ]);
    c.fillRect(0, 0, w, h);
    ridge(c, w, h * 0.68, h * 0.05, seed, "#78837d", 0.7, h);
    c.save();
    c.translate(w * 0.62, h * 0.4 + Math.sin(t * 0.5) * h * 0.012);
    c.rotate(0.2);
    c.fillStyle = LG(c, 0, -h * 0.3, 0, h * 0.3, [
      [0, "#2b302e"],
      [1, "#121514"],
    ]);
    c.beginPath();
    c.ellipse(0, 0, h * 0.07, h * 0.27, 0, 0, TAU);
    c.fill();
    c.restore();
    c.fillStyle = LG(c, 0, h * 0.45, 0, h * 0.8, [
      [0, "rgba(230,234,228,0)"],
      [0.5, "rgba(230,234,228,.55)"],
      [1, "rgba(230,234,228,0)"],
    ]);
    c.fillRect(0, 0, w, h);
    ridge(c, w, h * 0.82, h * 0.06, seed + 1, "#4c5650", 0.9, h);
    ridge(c, w, h * 0.94, h * 0.05, seed + 2, "#2b322e", 1.3, h);
  } else if (kind === "desert") {
    c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, "#f3c17c"],
      [0.6, "#f0954e"],
      [1, "#e06f37"],
    ]);
    c.fillRect(0, 0, w, h);
    const sx = w * 0.3,
      sy = h * 0.36,
      sr = h * 0.15;
    c.fillStyle = RG(c, sx, sy, sr * 0.8, sr * 3, [
      [0, "rgba(255,240,200,.6)"],
      [1, "rgba(255,240,200,0)"],
    ]);
    c.fillRect(0, 0, w, h);
    c.fillStyle = "#fff3d2";
    c.beginPath();
    c.arc(sx, sy, sr, 0, TAU);
    c.fill();
    c.fillStyle = "rgba(255,243,210,.6)";
    c.beginPath();
    c.arc(w * 0.42, h * 0.22, sr * 0.35, 0, TAU);
    c.fill();
    ridge(c, w, h * 0.62, h * 0.05, seed + 3, "#d47a40", 0.5, h);
    ridge(c, w, h * 0.74, h * 0.07, seed + 4, "#b65a2b", 0.7, h);
    ridge(c, w, h * 0.88, h * 0.06, seed + 5, "#86391c", 0.9, h);
  } else if (kind === "noir") {
    c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, "#1d2026"],
      [1, "#0a0b0d"],
    ]);
    c.fillRect(0, 0, w, h);
    c.fillStyle = RG(c, w * 0.2, h * 0.3, 0, h * 0.7, [
      [0, "rgba(255,222,165,.2)"],
      [1, "rgba(255,222,165,0)"],
    ]);
    c.fillRect(0, 0, w, h);
    c.save();
    c.globalAlpha = 0.2;
    c.fillStyle = "#f3e6c8";
    for (let i = 0; i < 9; i++) {
      const y0 = h * 0.06 + i * h * 0.075;
      c.beginPath();
      c.moveTo(w * 0.12, y0);
      c.lineTo(w * 0.7, y0 + h * 0.22);
      c.lineTo(w * 0.7, y0 + h * 0.255);
      c.lineTo(w * 0.12, y0 + h * 0.035);
      c.closePath();
      c.fill();
    }
    c.restore();
    const fx = w * 0.72,
      fy = h,
      fs = h * 0.6;
    c.fillStyle = "#040405";
    c.beginPath();
    c.moveTo(fx - fs * 0.45, fy);
    c.bezierCurveTo(
      fx - fs * 0.45,
      fy - fs * 0.45,
      fx - fs * 0.25,
      fy - fs * 0.55,
      fx,
      fy - fs * 0.56,
    );
    c.bezierCurveTo(
      fx + fs * 0.25,
      fy - fs * 0.55,
      fx + fs * 0.45,
      fy - fs * 0.45,
      fx + fs * 0.45,
      fy,
    );
    c.fill();
    c.beginPath();
    c.ellipse(fx, fy - fs * 0.7, fs * 0.1, fs * 0.13, 0, 0, TAU);
    c.fill();
    c.beginPath();
    c.ellipse(fx, fy - fs * 0.8, fs * 0.25, fs * 0.035, 0, 0, TAU);
    c.fill();
    rr(c, fx - fs * 0.13, fy - fs * 0.98, fs * 0.26, fs * 0.18, fs * 0.04);
    c.fill();
    c.fillStyle = `rgba(255,170,90,${0.5 + 0.5 * Math.sin(t * 6)})`;
    c.beginPath();
    c.arc(fx + fs * 0.08, fy - fs * 0.62, fs * 0.012, 0, TAU);
    c.fill();
  } else {
    /* dusk */ c.fillStyle = LG(c, 0, 0, 0, h, [
      [0, "#2b1b4a"],
      [0.55, "#c2507a"],
      [0.8, "#f39a6b"],
      [1, "#ffd29a"],
    ]);
    c.fillRect(0, 0, w, h);
    const sx = w * 0.64,
      sy = h * 0.6,
      sr = h * 0.12;
    c.fillStyle = RG(c, sx, sy, sr, sr * 4, [
      [0, "rgba(255,224,176,.5)"],
      [1, "rgba(255,224,176,0)"],
    ]);
    c.fillRect(0, 0, w, h);
    c.fillStyle = "#ffe0b0";
    c.beginPath();
    c.arc(sx, sy, sr, 0, TAU);
    c.fill();
    ridge(c, w, h * 0.62, h * 0.07, seed + 1, "#7a3b6e", 0.7, h);
    ridge(c, w, h * 0.72, h * 0.09, seed + 2, "#4f2a5a", 0.9, h);
    ridge(c, w, h * 0.83, h * 0.08, seed + 3, "#2e1a3e", 1.2, h);
    ridge(c, w, h * 0.94, h * 0.06, seed + 4, "#150d22", 1.6, h);
  }
  c.restore();
}
function poster(c, it, x, y, w, h, t) {
  art(c, it.art, hashStr(it.t), x, y, w, h, t);
  c.fillStyle = LG(c, 0, y + h * 0.5, 0, y + h, [
    [0, "rgba(0,0,0,0)"],
    [1, "rgba(0,0,0,.88)"],
  ]);
  c.fillRect(x, y, w, h);
  const o = { f: "Fraunces", w: 300, s: Math.round(w * 0.12) };
  const L = wrap(c, it.t, w - 36, o);
  L.forEach((ln, i) =>
    T(c, ln, x + 18, y + h - 24 - (L.length - 1 - i) * o.s * 1.1, { ...o, c: "#f4eedf" }),
  );
}
function programme(c, t, kind, title) {
  c.save();
  const k = 1.03 + 0.02 * Math.sin(t * 0.3);
  c.translate(960, 540);
  c.scale(k, k);
  c.translate(-960 + Math.sin(t * 0.2) * 20, -540);
  art(c, kind, hashStr(title), 0, 0, W, H, t);
  c.restore();
}

/* ============================ BLORP! =============================== */
function confetti(c, t, S, P, n) {
  for (let i = 0; i < n; i++) {
    const x = rnd(S, i) * W + Math.sin(t + i) * 30,
      y = ((rnd(S, i + 50) * (H + 100) + t * (40 + rnd(S, i + 60) * 70)) % (H + 100)) - 50,
      rot = t * (rnd(S, i + 70) - 0.5) * 4;
    c.save();
    c.translate(x, y);
    c.rotate(rot);
    c.fillStyle = [P.a, P.b, P.d, "#fff"][i % 4];
    c.globalAlpha = 0.85;
    const k = i % 3;
    if (k === 0) {
      c.beginPath();
      c.arc(0, 0, 9 + rnd(S, i + 80) * 9, 0, TAU);
      c.fill();
    } else if (k === 1) {
      c.strokeStyle = c.fillStyle;
      c.lineWidth = 8;
      c.lineCap = "round";
      c.beginPath();
      for (let j = 0; j < 5; j++) c.lineTo(j * 12 - 24, j % 2 ? -9 : 9);
      c.stroke();
    } else {
      c.beginPath();
      c.moveTo(0, -14);
      c.lineTo(13, 10);
      c.lineTo(-13, 10);
      c.closePath();
      c.fill();
    }
    c.restore();
  }
}
function sunburst(c, cx, cy, rot, n, col, a) {
  c.save();
  c.translate(cx, cy);
  c.rotate(rot);
  c.fillStyle = col;
  c.globalAlpha *= a;
  for (let i = 0; i < n; i++) {
    const a0 = (i / n) * TAU;
    c.beginPath();
    c.moveTo(0, 0);
    c.arc(0, 0, 2600, a0, a0 + Math.PI / n);
    c.closePath();
    c.fill();
  }
  c.restore();
}
function stripes(c, t, base, col, wd, ang) {
  c.fillStyle = base;
  c.fillRect(0, 0, W, H);
  c.save();
  c.translate(960, 540);
  c.rotate(ang);
  const off = (t * wd) % (wd * 2);
  c.fillStyle = col;
  for (let x = -2600 + off; x < 2600; x += wd * 2) c.fillRect(x, -1700, wd, 3400);
  c.restore();
}
function blinkAt(t, times) {
  for (const b of times) {
    const d = Math.abs(t - b);
    if (d < 0.09) return Math.max(0.08, d / 0.09);
  }
  return 1;
}
function eyes(c, x, y, er, t, P, sleep, blink, look = 0) {
  for (const s of [-1, 1]) {
    c.save();
    c.translate(x + s * er * 1.15, y);
    if (sleep) {
      c.strokeStyle = P.ink;
      c.lineWidth = 9;
      c.lineCap = "round";
      c.beginPath();
      c.arc(0, 0, er * 0.7, 0.15 * Math.PI, 0.85 * Math.PI);
      c.stroke();
    } else {
      c.scale(1, blink);
      c.fillStyle = "#fff";
      c.beginPath();
      c.arc(0, 0, er, 0, TAU);
      c.fill();
      c.lineWidth = 8;
      c.strokeStyle = P.ink;
      c.stroke();
      const lx = Math.sin(t * 1.7 + look) * er * 0.38,
        ly = Math.cos(t * 1.3) * er * 0.25;
      c.fillStyle = P.ink;
      c.beginPath();
      c.arc(lx, ly, er * 0.45, 0, TAU);
      c.fill();
      c.fillStyle = "#fff";
      c.beginPath();
      c.arc(lx - er * 0.15, ly - er * 0.15, er * 0.12, 0, TAU);
      c.fill();
    }
    c.restore();
  }
}
function mascot(c, x, y, r, t, P, o = {}) {
  const sq = o.sq || 0;
  c.save();
  c.translate(x, y);
  c.fillStyle = "rgba(0,0,0,.2)";
  c.beginPath();
  c.ellipse(0, r * 1.02, r * 0.8, r * 0.13, 0, 0, TAU);
  c.fill();
  c.scale(1 + sq, 1 - sq);
  blob(c, 0, 0, r, o.seed || 7, t, 0.05, 8);
  c.fillStyle = o.col;
  c.fill();
  c.lineWidth = 10;
  c.strokeStyle = P.ink;
  c.stroke();
  c.fillStyle = "rgba(255,255,255,.28)";
  c.beginPath();
  c.ellipse(-r * 0.38, -r * 0.42, r * 0.22, r * 0.11, -0.6, 0, TAU);
  c.fill();
  eyes(
    c,
    0,
    -r * 0.15,
    r * 0.24,
    t,
    P,
    o.sleep,
    o.sleep ? 1 : blinkAt(t, o.blinks || [2.2, 4.6]),
    o.seed || 0,
  );
  c.lineWidth = 10;
  c.strokeStyle = P.ink;
  c.lineCap = "round";
  c.beginPath();
  if (o.sleep) {
    c.arc(0, r * 0.32, r * 0.08, 0, TAU);
  } else {
    c.arc(0, r * 0.2, r * 0.28, 0.15 * Math.PI, 0.85 * Math.PI);
  }
  c.stroke();
  c.restore();
}
const kidsText = (P, s) => ({
  f: "Bungee",
  s,
  c: "#fff",
  sw: Math.round(s / 12),
  sc: P.ink,
  sh: P.ink,
  shx: Math.round(s / 20),
  shy: Math.round(s / 15),
  track: 4,
});
const KIDS = {
  ident: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      c.fillStyle = P.bg;
      c.fillRect(0, 0, W, H);
      sunburst(c, 960, 560, t * 0.12, 18, "#fff", 0.07);
      confetti(c, t, S, P, 36);
      const by = lerp(-520, 600, pr(t, 0, 0.6, E.inC)),
        land = t - 0.6;
      let sx = 0.88,
        sy = 1.14;
      if (land > 0) {
        const k = Math.exp(-land * 4.5) * Math.cos(land * 15);
        sx = 1 + 0.3 * k;
        sy = 1 - 0.3 * k;
      }
      if (land > 0)
        for (let i = 0; i < 4; i++) {
          const dx = [-300, -120, 110, 290][i],
            L = pr(t, 0.75 + i * 0.1, 2.4 + i * 0.2, E.outC) * [110, 190, 80, 150][i];
          c.fillStyle = P.ink;
          rr(c, 960 + dx - 31, by + 150, 62, L + 60, 31);
          c.fill();
          c.beginPath();
          c.arc(960 + dx, by + 190 + L, 39, 0, TAU);
          c.fill();
          c.fillStyle = P.a;
          rr(c, 960 + dx - 24, by + 150, 48, L + 60, 24);
          c.fill();
          c.beginPath();
          c.arc(960 + dx, by + 190 + L, 32, 0, TAU);
          c.fill();
        }
      c.save();
      c.translate(960, by + 270);
      c.scale(sx, sy);
      c.translate(0, -270);
      blob(c, 0, 0, 330, S, t, 0.05, 10, 1.55, 0.82);
      c.fillStyle = P.a;
      c.fill();
      c.lineWidth = 14;
      c.strokeStyle = P.ink;
      c.stroke();
      c.fillStyle = "rgba(255,255,255,.28)";
      c.beginPath();
      c.ellipse(-300, -130, 90, 34, -0.35, 0, TAU);
      c.fill();
      c.restore();
      const ep = pr(t, 1.8, 2.2, E.back);
      if (ep > 0) {
        c.save();
        c.translate(960, by - 275);
        c.scale(ep, ep);
        eyes(c, 0, 0, 64, t, P, false, blinkAt(t, [3.3, 5.0]));
        c.restore();
      }
      TA(c, "BLORP!", 960, by + 10, { ...kidsText(P, 170), a: "center" }, (i) => {
        const p = pr(t, 1.05 + i * 0.09, 1.55 + i * 0.09, E.back),
          w = pr(t, 2, 2.4);
        return {
          sc: p,
          rot:
            (rnd(S, i + 9) - 0.5) * 0.5 * (1 - p) +
            (rnd(S, i + 20) - 0.5) * 0.14 +
            Math.sin(t * 5 + i * 1.3) * 0.05 * w,
          dy: -Math.abs(Math.sin(t * 4.2 + i * 0.9)) * 14 * w,
        };
      });
      const tp = pr(t, 2.7, 3.3, E.back);
      if (tp > 0) {
        c.save();
        c.translate(960, lerp(1250, 975, tp));
        c.rotate(-0.03);
        c.fillStyle = P.ink;
        rr(c, -340, -52, 680, 104, 52);
        c.fill();
        T(c, "totally gooey TV", 0, 20, { f: "Fredoka", w: 700, s: 58, a: "center", c: P.d });
        c.restore();
      }
      if (t > 4.3) {
        const k = t - 4.3;
        for (let i = 0; i < 18; i++) {
          const a = rnd(S, i + 70) * TAU,
            sp = 500 + rnd(S, i + 90) * 800;
          c.fillStyle = [P.b, P.c, P.d][i % 3];
          c.beginPath();
          c.arc(
            960 + Math.cos(a) * sp * k,
            by + Math.sin(a) * sp * k + 700 * k * k,
            Math.max(4, 30 - k * 10),
            0,
            TAU,
          );
          c.fill();
        }
      }
    },
  },
  tonight: {
    dur: 7,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        L = D.e.list;
      c.fillStyle = P.bg;
      c.fillRect(0, 0, W, H);
      sunburst(c, 200, 1000, -t * 0.1, 14, "#fff", 0.06);
      confetti(c, t, S + 5, P, 22);
      TA(c, "COMING UP!", 1110, 180, { ...kidsText(P, 120), c: P.d, a: "center" }, (i) => ({
        sc: pr(t, 0.1 + i * 0.05, 0.5 + i * 0.05, E.back),
        dy: Math.sin(t * 5 + i) * 8,
      }));
      const cols = [P.a, P.c, P.b, P.a];
      L.forEach((it, i) => {
        const y = 355 + i * 185,
          p = pr(t, 0.5 + i * 0.22, 1.2 + i * 0.22, E.back);
        c.save();
        c.translate(lerp(1700, 0, p), 0);
        c.fillStyle = P.ink;
        rr(c, 470, y - 65, 1330, 150, 75);
        c.fill();
        c.fillStyle = cols[i];
        rr(c, 460, y - 75, 1330, 150, 75);
        c.fill();
        c.lineWidth = 10;
        c.strokeStyle = P.ink;
        c.stroke();
        c.fillStyle = P.d;
        c.beginPath();
        c.arc(540, y, 92, 0, TAU);
        c.fill();
        c.stroke();
        T(c, it.time, 540, y + 16, { f: "Bungee", s: 44, a: "center", c: P.ink });
        T(c, it.t, 670, y + 4, { f: "Fredoka", w: 700, s: 64, c: "#fff", sw: 9, sc: P.ink });
        T(c, it.sub, 672, y + 52, { f: "Fredoka", w: 600, s: 32, c: P.ink, alpha: 0.85 });
        if (i === 0) {
          c.save();
          c.translate(1720, y - 70);
          c.rotate(0.12);
          c.fillStyle = P.ink;
          rr(c, -70, -30, 140, 60, 30);
          c.fill();
          T(c, "NOW", 0, 16, { f: "Bungee", s: 40, a: "center", c: P.d });
          c.restore();
        }
        c.restore();
      });
      const b = Math.abs(Math.sin(t * 3.2));
      mascot(c, 230, 880 - b * 170, 130, t, P, { col: P.b, seed: S + 8, sq: (1 - b) ** 6 * 0.22 });
    },
  },
  upnext: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        e = D.e.next;
      stripes(c, t, P.c, hexA(P.ink, 0.12), 110, -0.35);
      confetti(c, t, S + 3, P, 16);
      const cp = pr(t, 0, 0.7, E.back);
      c.save();
      c.translate(lerp(-1400, 0, cp), 0);
      c.translate(640, 540);
      c.rotate(-0.03 + Math.sin(t * 2) * 0.008);
      c.translate(-640, -540);
      c.fillStyle = P.ink;
      rr(c, 126, 188, 1060, 740, 90);
      c.fill();
      c.fillStyle = P.a;
      rr(c, 110, 170, 1060, 740, 90);
      c.fill();
      c.lineWidth = 12;
      c.strokeStyle = P.ink;
      c.stroke();
      TA(c, "UP NEXT!", 640, 320, { ...kidsText(P, 120), a: "center" }, (i) => ({
        dy: Math.sin(t * 6 + i * 0.8) * 10,
        rot: Math.sin(t * 3 + i) * 0.05,
        sc: pr(t, 0.4 + i * 0.05, 0.8 + i * 0.05, E.back),
      }));
      const lines = wrap(c, e.t, 940, { f: "Fredoka", w: 700, s: 92 });
      lines.forEach((ln, i) =>
        T(c, ln, 170, 505 + i * 100, {
          f: "Fredoka",
          w: 700,
          s: 92,
          c: "#fff",
          sw: 10,
          sc: P.ink,
          alpha: pr(t, 0.7 + i * 0.1, 1.1 + i * 0.1),
        }),
      );
      T(c, e.sub, 170, 505 + lines.length * 100 + 10, {
        f: "Fredoka",
        w: 600,
        s: 44,
        c: P.ink,
        alpha: pr(t, 1, 1.4),
      });
      c.restore();
      const pp = pr(t, 0.5, 1.1, E.back);
      if (pp > 0) {
        c.save();
        c.translate(1500, 560);
        c.rotate(0.06 + Math.sin(t * 1.5) * 0.02);
        c.scale(pp, pp);
        c.fillStyle = P.ink;
        rr(c, -316, -306, 660, 660, 46);
        c.fill();
        c.fillStyle = "#fff";
        rr(c, -330, -330, 660, 660, 46);
        c.fill();
        c.save();
        rr(c, -300, -300, 600, 600, 30);
        c.clip();
        art(c, e.art, hashStr(e.t), -300, -300, 600, 600, t);
        c.restore();
        c.restore();
      }
      const bp = pr(t, 1.1, 1.5, E.back);
      if (bp > 0) {
        c.save();
        c.translate(1770, 230);
        c.scale(bp, bp);
        c.save();
        c.rotate(t * 0.4);
        starBadge(c, 0, 0, 130, P.d, P.ink);
        c.restore();
        T(c, e.time, 0, 10, { f: "Bungee", s: 62, a: "center", c: P.ink });
        T(c, "PM", 0, 58, { f: "Fredoka", w: 700, s: 34, a: "center", c: P.ink });
        c.restore();
      }
      mascot(c, 1185, 1120 - pr(t, 1.6, 2.2, E.back) * 230, 150, t, P, { col: P.b, seed: S + 4 });
    },
  },
  overlay: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e;
      programme(c, t, e.now.art, e.now.t);
      const ph = t % 3,
        wig = ph > 0.2 ? Math.sin((ph - 0.2) * 20) * Math.exp(-(ph - 0.2) * 4) * 0.25 : 0;
      c.save();
      c.translate(1780, 130);
      c.rotate(wig);
      c.globalAlpha = 0.94;
      blob(c, 0, 0, 70, D.seed, t, 0.06, 8, 1.25, 1);
      c.fillStyle = P.a;
      c.fill();
      c.lineWidth = 7;
      c.strokeStyle = P.ink;
      c.stroke();
      T(c, "B!", 0, 18, { f: "Bungee", s: 50, a: "center", c: "#fff", sw: 6, sc: P.ink });
      c.restore();
      const off = (1 - pr(t, 0.8, 1.3, E.back)) * 280 + pr(t, 5.6, 6.1, E.inC) * 280;
      c.save();
      c.translate(0, off);
      c.fillStyle = P.ink;
      rr(c, 110, 850, 1080, 110, 55);
      c.fill();
      c.fillStyle = P.d;
      rr(c, 124, 864, 190, 82, 41);
      c.fill();
      T(c, "NOW", 219, 922, { f: "Bungee", s: 46, a: "center", c: P.ink });
      T(c, e.now.t, 345, 924, { f: "Fredoka", w: 700, s: 56, c: "#fff" });
      c.fillStyle = P.a;
      rr(c, 150, 972, 800, 66, 33);
      c.fill();
      c.lineWidth = 6;
      c.strokeStyle = P.ink;
      c.stroke();
      T(c, `NEXT ${e.next.time} · ${e.next.t}`, 180, 1017, {
        f: "Fredoka",
        w: 700,
        s: 34,
        c: "#fff",
      });
      c.restore();
    },
  },
  break: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      c.fillStyle = P.c;
      c.fillRect(0, 0, W, H);
      sunburst(c, 560, 600, t * 0.5, 20, "#fff", 0.13);
      confetti(c, t, S + 9, P, 26);
      const mp = pr(t, 0, 0.5, E.back);
      c.save();
      c.translate(560, 600);
      c.rotate(Math.sin(t * 2.4) * 0.08);
      c.scale(mp, mp);
      mascot(c, 0, 0, 250, t, P, { col: P.a, seed: S + 2, blinks: [1.9, 4.1] });
      c.restore();
      ["WE'LL BE", "RIGHT", "BACK!"].forEach((w, j) =>
        TA(
          c,
          w,
          1330,
          300 + j * 190,
          { ...kidsText(P, j ? 170 : 110), c: j === 2 ? P.d : "#fff", a: "center" },
          (i) => ({
            sc: pr(t, 0.3 + j * 0.25 + i * 0.05, 0.7 + j * 0.25 + i * 0.05, E.back),
            rot: Math.sin(t * 4 + i * 1.7 + j) * 0.07,
            dy: Math.sin(t * 5 + i + j) * 6,
          }),
        ),
      );
      const pp = pr(t, 1.5, 2, E.back);
      if (pp > 0) {
        c.save();
        c.translate(1330, 950);
        c.scale(pp, pp);
        c.rotate(-0.02);
        c.fillStyle = P.ink;
        rr(c, -380, -48, 760, 96, 48);
        c.fill();
        T(c, D.e.breakNext, 0, 16, { f: "Fredoka", w: 700, s: 44, a: "center", c: P.d });
        c.restore();
      }
    },
  },
  back: {
    dur: 4.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        e = D.e.now;
      c.fillStyle = P.bg;
      c.fillRect(0, 0, W, H);
      blob(c, 960, 540, 1350 * pr(t, 0, 0.8, E.outC) + 1, S + 11, t, 0.08, 12);
      c.fillStyle = P.a;
      c.fill();
      blob(c, 960, 540, 1150 * pr(t, 0.15, 0.95, E.outC) + 1, S + 12, t, 0.07, 11);
      c.fillStyle = P.d;
      c.fill();
      confetti(c, t, S + 13, P, 20);
      TA(c, "AND NOW", 960, 320, { ...kidsText(P, 110), a: "center" }, (i) => ({
        sc: pr(t, 0.6 + i * 0.05, 1 + i * 0.05, E.back),
        rot: Math.sin(t * 4 + i) * 0.05,
      }));
      TA(c, "BACK TO", 960, 470, { ...kidsText(P, 110), c: P.bg, a: "center" }, (i) => ({
        sc: pr(t, 1 + i * 0.05, 1.4 + i * 0.05, E.back),
        rot: Math.sin(t * 4 + i + 2) * 0.05,
      }));
      const p = pr(t, 1.6, 2.2, E.back);
      if (p > 0) {
        c.save();
        c.translate(960, 700);
        c.scale(p, p);
        c.rotate(Math.sin(t * 3) * 0.02);
        wrap(c, e.t, 1500, { f: "Fredoka", w: 700, s: 140 }).forEach((ln, i) =>
          T(c, ln, 0, i * 150, {
            f: "Fredoka",
            w: 700,
            s: 140,
            a: "center",
            c: "#fff",
            sw: 14,
            sc: P.ink,
          }),
        );
        c.restore();
      }
    },
  },
  offair: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      c.fillStyle = LG(c, 0, 0, 0, H, [
        [0, P.ink],
        [1, P.bg],
      ]);
      c.fillRect(0, 0, W, H);
      for (let i = 0; i < 70; i++) {
        c.fillStyle = `rgba(255,255,255,${0.3 + 0.7 * Math.abs(Math.sin(t * 1.5 + i * 2.1))})`;
        star4(c, rnd(S, i) * W, rnd(S, i + 90) * H * 0.7, 4 + rnd(S, i + 40) * 9, 0, c.fillStyle);
      }
      crescent(c, 1520, 240, 110, P.d);
      mascot(c, 960, 610, 200, t, P, {
        col: P.a,
        seed: S + 2,
        sleep: true,
        sq: Math.sin(t * 1.6) * 0.04,
      });
      for (let k = 0; k < 3; k++) {
        const ph = (t * 0.45 + k / 3) % 1;
        T(c, "Z", 1150 + ph * 130, 430 - ph * 260, {
          f: "Bungee",
          s: 40 + ph * 50,
          c: "#fff",
          alpha: Math.sin(ph * Math.PI),
          sw: 6,
          sc: P.ink,
        });
      }
      T(c, "BLORP! is napping", 960, 925, {
        f: "Bungee",
        s: 78,
        a: "center",
        c: "#fff",
        sw: 10,
        sc: P.ink,
      });
      T(c, D.e.offair, 960, 1005, { f: "Fredoka", w: 700, s: 44, a: "center", c: P.d });
    },
  },
};

/* =========================== NOCTURNE ============================== */
function fog(c, t, col, S, n = 7, y0 = 760, sp = 300, a = 0.16) {
  for (let i = 0; i < n; i++) {
    const rx = 500 + rnd(S, i) * 500,
      ry = 110 + rnd(S, i + 9) * 90,
      x = ((rnd(S, i + 20) * (W + 1000) + t * (18 + rnd(S, i + 30) * 30)) % (W + 1000)) - 500,
      y = y0 + (rnd(S, i + 40) - 0.5) * sp;
    c.save();
    c.translate(x, y);
    c.scale(1, ry / rx);
    c.fillStyle = RG(c, 0, 0, 0, rx, [
      [0, hexA(col, a)],
      [1, hexA(col, 0)],
    ]);
    c.fillRect(-rx, -rx, rx * 2, rx * 2);
    c.restore();
  }
}
function moon(c, x, y, r, col, a) {
  c.save();
  c.globalAlpha *= a;
  c.fillStyle = RG(c, x, y, r * 0.9, r * 3.4, [
    [0, hexA(col, 0.28)],
    [1, hexA(col, 0)],
  ]);
  c.fillRect(0, 0, W, H);
  c.fillStyle = col;
  c.beginPath();
  c.arc(x, y, r, 0, TAU);
  c.fill();
  c.fillStyle = "rgba(60,60,70,.1)";
  [
    [-0.3, -0.2, 0.22],
    [0.25, 0.1, 0.16],
    [-0.05, 0.4, 0.12],
    [0.35, -0.35, 0.1],
  ].forEach(([dx, dy, k]) => {
    c.beginPath();
    c.arc(x + dx * r, y + dy * r, k * r, 0, TAU);
    c.fill();
  });
  c.restore();
}
function bat(c, x, y, s, ph) {
  const f = Math.sin(ph);
  c.save();
  c.translate(x, y);
  c.scale(s, s);
  c.fillStyle = "#000";
  for (const m of [1, -1]) {
    c.save();
    c.scale(m, 1);
    c.beginPath();
    c.moveTo(0, -4);
    c.quadraticCurveTo(30, -30 * f - 14, 80, -40 * f - 4);
    c.quadraticCurveTo(72, -12 * f + 6, 64, 4);
    c.quadraticCurveTo(54, -2, 46, 10);
    c.quadraticCurveTo(36, 4, 26, 14);
    c.quadraticCurveTo(14, 6, 0, 10);
    c.closePath();
    c.fill();
    c.restore();
  }
  c.beginPath();
  c.ellipse(0, 2, 9, 15, 0, 0, TAU);
  c.fill();
  c.beginPath();
  c.moveTo(-7, -10);
  c.lineTo(-4, -22);
  c.lineTo(0, -12);
  c.lineTo(4, -22);
  c.lineTo(7, -10);
  c.fill();
  c.restore();
}
function typeA(t, start, i, step, S) {
  const ti = start + i * step;
  if (t < ti) return 0;
  if (t - ti < 0.12) return rnd(S, i * 31 + Math.floor(t * 40)) > 0.35 ? 1 : 0.25;
  return 1;
}
function figure(c, x, y, h, a) {
  c.save();
  c.globalAlpha *= a;
  c.fillStyle = "#000";
  c.beginPath();
  c.arc(x, y - h * 0.9, h * 0.07, 0, TAU);
  c.fill();
  c.beginPath();
  c.moveTo(x - h * 0.11, y - h * 0.8);
  c.lineTo(x + h * 0.11, y - h * 0.8);
  c.quadraticCurveTo(x + h * 0.16, y - h * 0.78, x + h * 0.17, y - h * 0.6);
  c.lineTo(x + h * 0.15, y);
  c.lineTo(x - h * 0.15, y);
  c.lineTo(x - h * 0.17, y - h * 0.6);
  c.quadraticCurveTo(x - h * 0.16, y - h * 0.78, x - h * 0.11, y - h * 0.8);
  c.fill();
  c.restore();
}
const NOCT = {
  ident: {
    dur: 7,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      c.fillStyle = P.bg;
      c.fillRect(0, 0, W, H);
      const my = lerp(520, 330, pr(t, 0, 3.2, E.outC));
      moon(c, 1390, my, 150, P.moon, pr(t, 0, 1.5));
      const cx = lerp(700, 2100, pr(t, 0, 7));
      c.save();
      c.translate(cx, my + 40);
      c.scale(1, 0.22);
      c.fillStyle = RG(c, 0, 0, 0, 380, [
        [0, "rgba(5,4,8,.92)"],
        [1, "rgba(5,4,8,0)"],
      ]);
      c.fillRect(-380, -380, 760, 760);
      c.restore();
      fog(c, t, P.fog, S, 8, 820, 240, 0.14);
      for (let i = 0; i < 5; i++) {
        const st = 1.2 + i * 0.35 + rnd(S, i) * 0.3,
          k = (t - st) / 2.8;
        if (k < 0 || k > 1) continue;
        bat(
          c,
          lerp(2050, -150, k),
          250 + rnd(S, i + 5) * 220 + Math.sin(k * 9 + i) * 30,
          0.8 + rnd(S, i + 7) * 0.6,
          t * 18 + i,
        );
      }
      TA(
        c,
        "NOCTURNE",
        960,
        640,
        { f: "Playfair", w: 900, s: 190, c: P.bone, track: 26, a: "center", b: "alphabetic" },
        (i) => ({ al: typeA(t, 2.4, i, 0.11, S) }),
      );
      const rw = pr(t, 3.5, 4.3, E.io) * 460;
      c.fillStyle = P.red;
      c.fillRect(960 - rw, 690, rw * 2, 5);
      T(c, "LATE NIGHT  ·  LIGHTS OFF", 960, 775, {
        f: "Elite",
        s: 40,
        a: "center",
        c: P.bone,
        track: 10,
        alpha: pr(t, 4.3, 5) * 0.85,
      });
    },
  },
  tonight: {
    dur: 7.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        L = D.e.list;
      c.fillStyle = P.bg;
      c.fillRect(0, 0, W, H);
      const ang = Math.sin(t * 1.3) * 0.22,
        bx = 960 + Math.sin(ang) * 300,
        by = -60 + Math.cos(ang) * 300;
      c.fillStyle = RG(c, bx, by + 40, 0, 950, [
        [0, hexA(P.bone, 0.24)],
        [1, hexA(P.bone, 0)],
      ]);
      c.fillRect(0, 0, W, H);
      c.strokeStyle = "#2a2a2a";
      c.lineWidth = 3;
      c.beginPath();
      c.moveTo(960, -60);
      c.lineTo(bx, by);
      c.stroke();
      c.fillStyle = "#1a1a1a";
      c.save();
      c.translate(bx, by);
      c.rotate(-ang);
      c.fillRect(-12, -4, 24, 20);
      c.restore();
      c.save();
      c.shadowColor = P.bone;
      c.shadowBlur = 70;
      c.fillStyle = "#fff6e0";
      c.beginPath();
      c.arc(bx - Math.sin(ang) * 30, by + Math.cos(ang) * 30, 24, 0, TAU);
      c.fill();
      c.restore();
      fog(c, t, P.fog, S + 1, 6, 980, 120, 0.12);
      T(c, "TONIGHT ON NOCTURNE", 960, 410, {
        f: "Elite",
        s: 40,
        a: "center",
        c: P.red,
        track: 14,
        alpha: pr(t, 0.2, 0.8),
      });
      const lit = 0.6 + 0.4 * Math.max(0, 1 - Math.abs(bx - 960) / 400);
      L.forEach((it, i) => {
        const y = 535 + i * 140,
          st = 0.7 + i * 0.55;
        TA(
          c,
          it.time,
          780,
          y,
          { f: "Elite", s: 40, a: "right", c: P.red, b: "alphabetic", track: 2 },
          (j) => ({ al: typeA(t, st, j, 0.05, S + i) * lit }),
        );
        const w = TA(
          c,
          it.t,
          830,
          y,
          { f: "Playfair", w: 900, s: 70, a: "left", c: P.bone, b: "alphabetic" },
          (j) => ({ al: typeA(t, st + 0.25, j, 0.035, S + i * 7) * lit }),
        );
        T(c, it.year, 830 + w + 24, y, {
          f: "Elite",
          s: 34,
          c: P.bone,
          alpha: pr(t, st + 0.9, st + 1.3) * 0.55 * lit,
        });
        if (i === 0) {
          const pa = pr(t, st, st + 0.3) * (0.6 + 0.4 * Math.sin(t * 5));
          c.fillStyle = hexA(P.red, pa);
          c.beginPath();
          c.arc(552, y - 13, 10, 0, TAU);
          c.fill();
          T(c, "NOW", 528, y, { f: "Elite", s: 28, a: "right", c: P.red, alpha: pa });
        }
      });
    },
  },
  upnext: {
    dur: 7,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        e = D.e.next;
      c.fillStyle = "#000";
      c.fillRect(0, 0, W, H);
      c.save();
      const k = 1.06 + 0.07 * pr(t, 0, 7);
      c.translate(960, 540);
      c.scale(k, k);
      c.translate(-960, -540);
      art(c, e.art, hashStr(e.t), 0, 0, W, H, t);
      c.restore();
      c.save();
      c.globalCompositeOperation = "multiply";
      c.fillStyle = hexA(P.red, 0.4);
      c.fillRect(0, 0, W, H);
      c.restore();
      c.fillStyle = LG(c, 0, 0, W * 0.75, 0, [
        [0, "rgba(0,0,0,.95)"],
        [0.6, "rgba(0,0,0,.6)"],
        [1, "rgba(0,0,0,0)"],
      ]);
      c.fillRect(0, 0, W, H);
      TA(
        c,
        "UP NEXT",
        140,
        250,
        { f: "Elite", s: 44, c: P.red, track: 12, a: "left", b: "alphabetic" },
        (i) => ({ al: typeA(t, 0.3, i, 0.07, S) }),
      );
      c.fillStyle = P.red;
      c.fillRect(140, 280, pr(t, 0.8, 1.3, E.outC) * 140, 4);
      const lines = wrap(c, e.t, 1150, { f: "Playfair", w: 900, s: 128 });
      c.save();
      c.beginPath();
      c.rect(0, 0, lerp(100, 1500, pr(t, 0.9, 1.9, E.io)), H);
      c.clip();
      const fl = t < 2.1 ? (rnd(S, Math.floor(t * 30)) > 0.3 ? 1 : 0.5) : 1;
      lines.forEach((ln, i) =>
        T(c, ln, 135, 440 + i * 135, { f: "Playfair", w: 900, s: 128, c: P.bone, alpha: fl }),
      );
      c.restore();
      const y = 440 + (lines.length - 1) * 135 + 90;
      T(c, e.meta, 140, y, {
        f: "Elite",
        s: 38,
        c: P.bone,
        alpha: pr(t, 1.9, 2.5) * 0.8,
        track: 2,
      });
      wrap(c, e.log, 980, { f: "Playfair", i: 1, s: 44 }).forEach((ln, i) =>
        T(c, ln, 140, y + 90 + i * 60, {
          f: "Playfair",
          i: 1,
          s: 44,
          c: P.bone,
          alpha: pr(t, 2.4 + i * 0.2, 3.1 + i * 0.2) * 0.92,
        }),
      );
      T(c, "TONIGHT AT", 1780, 860, {
        f: "Elite",
        s: 34,
        a: "right",
        c: P.bone,
        track: 8,
        alpha: pr(t, 3, 3.6) * 0.7,
      });
      T(c, e.time, 1780, 965, { f: "Elite", s: 110, a: "right", c: P.red, alpha: pr(t, 3.2, 3.8) });
      c.fillStyle = P.red;
      c.fillRect(140, 1030, (1 - pr(t, 0, 7)) * 1640, 3);
    },
  },
  overlay: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        e = D.e;
      programme(c, t, e.now.art, e.now.t);
      c.fillStyle = LG(c, 0, 740, 0, H, [
        [0, "rgba(0,0,0,0)"],
        [1, "rgba(0,0,0,.8)"],
      ]);
      c.fillRect(0, 0, W, H);
      c.save();
      c.globalAlpha = 0.55 * (0.85 + 0.15 * rnd(S, Math.floor(t * 24)));
      c.strokeStyle = P.bone;
      c.lineWidth = 3;
      c.beginPath();
      c.arc(1790, 120, 46, 0, TAU);
      c.stroke();
      T(c, "N", 1790, 122, { f: "Playfair", w: 900, s: 56, a: "center", b: "middle", c: P.bone });
      c.restore();
      c.save();
      c.globalAlpha = 1 - pr(t, 5.6, 6.1);
      c.fillStyle = P.red;
      c.fillRect(140, 925, pr(t, 0.8, 1.4, E.outC) * 900, 3);
      TA(
        c,
        `NOW · ${e.now.t.toUpperCase()} (${e.now.year})`,
        140,
        900,
        { f: "Elite", s: 40, c: P.bone, track: 4, a: "left", b: "alphabetic" },
        (i) => ({ al: typeA(t, 1, i, 0.03, S) }),
      );
      T(c, e.nextLine, 140, 980, { f: "Elite", s: 30, c: P.red, track: 3, alpha: pr(t, 1.9, 2.4) });
      c.restore();
    },
  },
  break: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      c.fillStyle = "#030303";
      c.fillRect(0, 0, W, H);
      const fx = 760,
        fy = 180,
        fw = 400,
        fh = 660,
        a = pr(t, 0.5, 3.2, E.io) * 1.15,
        open = Math.sin(a);
      c.save();
      c.globalAlpha = open;
      c.fillStyle = RG(c, 960, 510, 0, 950, [
        [0, hexA(P.bone, 0.25)],
        [1, hexA(P.bone, 0)],
      ]);
      c.fillRect(0, 0, W, H);
      c.fillStyle = LG(c, 0, fy + fh, 0, H, [
        [0, hexA(P.bone, 0.42)],
        [1, hexA(P.bone, 0)],
      ]);
      c.beginPath();
      c.moveTo(fx, fy + fh);
      c.lineTo(fx + fw, fy + fh);
      c.lineTo(fx + fw + 380, H);
      c.lineTo(fx - 380, H);
      c.closePath();
      c.fill();
      c.restore();
      c.fillStyle = "#0b0907";
      c.fillRect(fx, fy, fw, fh);
      c.fillStyle = hexA(P.bone, 0.92 * open);
      c.fillRect(fx, fy, fw, fh);
      figure(c, fx + fw * 0.55, fy + fh, 520, pr(t, 3.3, 4.3));
      const xe = fx + fw * Math.cos(a);
      c.fillStyle = LG(c, fx, 0, fx + fw, 0, [
        [0, "#1d1612"],
        [1, "#120d0a"],
      ]);
      c.beginPath();
      c.moveTo(fx, fy);
      c.lineTo(xe, fy + 26 * open);
      c.lineTo(xe, fy + fh - 26 * open);
      c.lineTo(fx, fy + fh);
      c.closePath();
      c.fill();
      c.strokeStyle = "rgba(255,255,255,.05)";
      c.lineWidth = 3;
      [
        [0.12, 0.08, 0.38],
        [0.12, 0.52, 0.4],
      ].forEach(([u, v, hh]) => {
        const x1 = lerp(fx, xe, u),
          x2 = lerp(fx, xe, 1 - u);
        c.strokeRect(x1, fy + fh * v, x2 - x1, fh * hh);
      });
      c.fillStyle = "#6b5a3a";
      c.beginPath();
      c.arc(lerp(fx, xe, 0.86), fy + fh * 0.5, 9, 0, TAU);
      c.fill();
      c.strokeStyle = "#120e0b";
      c.lineWidth = 20;
      c.strokeRect(fx - 10, fy - 10, fw + 20, fh + 10);
      TA(
        c,
        "Don't go anywhere.",
        960,
        960,
        { f: "Playfair", i: 1, s: 64, a: "center", b: "alphabetic", c: P.bone },
        (i) => ({ al: typeA(t, 4.2, i, 0.04, S) }),
      );
      T(c, D.e.breakNext, 960, 1030, {
        f: "Elite",
        s: 32,
        a: "center",
        c: P.red,
        track: 4,
        alpha: pr(t, 5, 5.5),
      });
    },
  },
  back: {
    dur: 4.8,
    draw(c, t, D) {
      const P = D.P,
        e = D.e.now;
      if (t < 3) {
        const n = 3 - Math.floor(t),
          fr = t % 1;
        c.fillStyle = RG(c, 960, 540, 0, 1100, [
          [0, "#a39d92"],
          [1, "#57534c"],
        ]);
        c.fillRect(0, 0, W, H);
        c.fillStyle = "rgba(0,0,0,.28)";
        c.beginPath();
        c.moveTo(960, 540);
        c.arc(960, 540, 1400, -Math.PI / 2, -Math.PI / 2 + TAU * fr);
        c.closePath();
        c.fill();
        c.strokeStyle = "#eee";
        c.lineWidth = 6;
        c.beginPath();
        c.moveTo(0, 540);
        c.lineTo(W, 540);
        c.moveTo(960, 0);
        c.lineTo(960, H);
        c.stroke();
        for (const r of [300, 360]) {
          c.beginPath();
          c.arc(960, 540, r, 0, TAU);
          c.stroke();
        }
        T(c, String(n), 960, 560, {
          f: "Playfair",
          w: 900,
          s: 440,
          a: "center",
          b: "middle",
          c: "#111",
        });
      } else {
        const k = t - 3;
        c.fillStyle = "#000";
        c.fillRect(0, 0, W, H);
        if (k < 0.14) {
          c.fillStyle = `rgba(255,255,255,${1 - k / 0.14})`;
          c.fillRect(0, 0, W, H);
        }
        T(c, "AND NOW, BACK TO", 960, 450, {
          f: "Elite",
          s: 40,
          a: "center",
          c: P.red,
          track: 12,
          alpha: pr(k, 0.1, 0.5),
        });
        wrap(c, e.t, 1500, { f: "Playfair", w: 900, s: 130 }).forEach((ln, i) =>
          T(c, ln, 960, 600 + i * 140, {
            f: "Playfair",
            w: 900,
            s: 130,
            a: "center",
            c: P.bone,
            alpha: pr(k, 0.3, 0.9),
          }),
        );
      }
    },
  },
  offair: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P;
      c.fillStyle = "#7a7a7a";
      c.fillRect(0, 0, W, H);
      grain(c, t * 2, 1, "source-over");
      c.fillStyle = "rgba(0,0,0,.35)";
      c.fillRect(0, 0, W, H);
      const y = ((t * 260) % (H + 300)) - 150;
      c.fillStyle = LG(c, 0, y, 0, y + 220, [
        [0, "rgba(0,0,0,0)"],
        [0.5, "rgba(0,0,0,.45)"],
        [1, "rgba(0,0,0,0)"],
      ]);
      c.fillRect(0, y, W, 220);
      c.fillStyle = "rgba(0,0,0,.9)";
      rr(c, 520, 350, 880, 380, 8);
      c.fill();
      c.strokeStyle = hexA(P.bone, 0.5);
      c.lineWidth = 2;
      c.stroke();
      T(c, "NOCTURNE", 960, 480, {
        f: "Playfair",
        w: 900,
        s: 96,
        a: "center",
        c: P.bone,
        track: 10,
      });
      T(c, "THE CHANNEL SLEEPS.", 960, 570, {
        f: "Elite",
        s: 40,
        a: "center",
        c: P.bone,
        track: 6,
      });
      T(c, D.e.offair, 960, 650, { f: "Elite", s: 32, a: "center", c: P.red });
    },
  },
};
function noirPost(c, t, D) {
  const f = Math.floor(t * 24);
  grain(c, t, 0.3);
  c.fillStyle = `rgba(0,0,0,${0.04 + 0.1 * rnd(D.seed, f)})`;
  c.fillRect(0, 0, W, H);
  if (rnd(D.seed, f + 999) > 0.72) {
    c.fillStyle = "rgba(240,230,210,.16)";
    c.fillRect(rnd(D.seed, f + 5) * W, 0, 2, H);
  }
  vignette(c, 0.8);
}

/* =========================== MERIDIAN ============================== */
function mbg(c, P) {
  c.fillStyle = RG(c, 960, 480, 0, 1300, [
    [0, P.bg2],
    [1, P.bg],
  ]);
  c.fillRect(0, 0, W, H);
}
function dust(c, t, S, n, col) {
  for (let i = 0; i < n; i++) {
    const x = rnd(S, i) * W + Math.sin(t * 0.4 + i) * 30,
      y = (((rnd(S, i + 50) * H - t * (6 + rnd(S, i + 60) * 14)) % H) + H) % H,
      a = (0.15 + 0.5 * rnd(S, i + 70)) * (0.5 + 0.5 * Math.sin(t * 1.5 + i));
    c.fillStyle = hexA(col, a);
    c.beginPath();
    c.arc(x, y, 1.5 + rnd(S, i + 80) * 3, 0, TAU);
    c.fill();
  }
}
function beam(c, P, a) {
  c.save();
  c.globalAlpha = a;
  c.fillStyle = LG(c, 200, 0, 900, 1080, [
    [0, hexA(P.gold, 0.55)],
    [1, hexA(P.gold, 0)],
  ]);
  c.beginPath();
  c.moveTo(150, -50);
  c.lineTo(520, -50);
  c.lineTo(1500, H + 50);
  c.lineTo(700, H + 50);
  c.closePath();
  c.fill();
  c.restore();
}
const MER = {
  ident: {
    dur: 7,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      mbg(c, P);
      beam(c, P, 0.09 * pr(t, 0, 2));
      dust(c, t, S, 70, P.gold);
      const cx = 960,
        cy = 430,
        R = 160;
      c.strokeStyle = P.gold;
      c.lineWidth = 3;
      c.beginPath();
      c.arc(cx, cy, R, -Math.PI / 2, -Math.PI / 2 + TAU * pr(t, 0.3, 2, E.io));
      c.stroke();
      const ll = pr(t, 1.5, 2.6, E.io) * 420;
      c.fillStyle = LG(c, cx - R - ll, 0, cx - R, 0, [
        [0, hexA(P.gold, 0)],
        [1, P.gold],
      ]);
      c.fillRect(cx - R - ll, cy - 1, ll, 2);
      c.fillStyle = LG(c, cx + R + ll, 0, cx + R, 0, [
        [0, hexA(P.gold, 0)],
        [1, P.gold],
      ]);
      c.fillRect(cx + R, cy - 1, ll, 2);
      const sp = pr(t, 1.4, 2.2, E.back);
      if (sp > 0) {
        c.save();
        c.shadowColor = P.gold;
        c.shadowBlur = 30 * sp;
        star4(c, cx, cy, 70 * sp, t * 0.15, P.gold);
        c.restore();
      }
      const gx = lerp(250, 1700, pr(t, 4, 5.4, E.io)),
        g = LG(c, gx - 260, 0, gx + 260, 0, [
          [0, P.ivory],
          [0.42, P.ivory],
          [0.5, "#fffaf0"],
          [0.58, P.ivory],
          [1, P.ivory],
        ]);
      const glow = Math.sin(Math.PI * pr(t, 4, 5.4));
      T(c, "MERIDIAN", 960, 760, {
        f: "Fraunces",
        w: 300,
        s: 150,
        a: "center",
        c: g,
        track: lerp(70, 26, pr(t, 2, 3.8, E.outC)),
        alpha: pr(t, 2, 3),
        glow: glow > 0.05 ? hexA(P.gold, 0.5 * glow) : null,
        gb: 30,
      });
      T(c, "CINEMA", 960, 840, {
        f: "Inter",
        w: 600,
        s: 30,
        a: "center",
        c: P.gold,
        track: 22,
        alpha: pr(t, 3.1, 3.9),
      });
    },
  },
  tonight: {
    dur: 8,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        L = D.e.list;
      mbg(c, P);
      dust(c, t, S, 40, P.gold);
      T(c, "TONIGHT ON MERIDIAN", 960, 150, {
        f: "Inter",
        w: 600,
        s: 26,
        a: "center",
        c: P.gold,
        track: 12,
        alpha: pr(t, 0.1, 0.7),
      });
      T(c, D.e.blockName, 960, 250, {
        f: "Fraunces",
        w: 300,
        s: 84,
        a: "center",
        c: P.ivory,
        alpha: pr(t, 0.3, 1.1),
      });
      const xs = [480, 800, 1120, 1440],
        lp = pr(t, 0.6, 1.8, E.io);
      c.fillStyle = hexA(P.gold, 0.5);
      c.fillRect(960 - 640 * lp, 400, 1280 * lp, 2);
      L.forEach((it, i) => {
        const x = xs[i],
          p = pr(t, 0.9 + i * 0.22, 1.9 + i * 0.22, E.outQ);
        c.save();
        c.globalAlpha = p;
        c.fillStyle = P.gold;
        c.beginPath();
        c.arc(x, 400, 8, 0, TAU);
        c.fill();
        if (i === 0) {
          const pl = (t * 0.8) % 1;
          c.strokeStyle = hexA(P.gold, 1 - pl);
          c.lineWidth = 2;
          c.beginPath();
          c.arc(x, 400, 8 + pl * 30, 0, TAU);
          c.stroke();
        }
        c.restore();
        T(c, it.time, x, 370, {
          f: "Inter",
          w: 600,
          s: 30,
          a: "center",
          c: i === 0 ? P.gold : P.ivory,
          alpha: p,
        });
        c.save();
        c.globalAlpha = p;
        c.translate(0, (1 - p) * 60);
        c.shadowColor = "rgba(0,0,0,.6)";
        c.shadowBlur = 40;
        c.shadowOffsetY = 20;
        c.fillStyle = "#000";
        c.fillRect(x - 150, 450, 300, 450);
        c.restore();
        c.save();
        c.globalAlpha = p;
        c.translate(0, (1 - p) * 60);
        poster(c, it, x - 150, 450, 300, 450, t);
        c.restore();
        T(c, i === 0 ? "NOW" : it.year, x, 955, {
          f: "Inter",
          w: 600,
          s: 24,
          a: "center",
          c: i === 0 ? P.gold : P.ivory,
          track: 6,
          alpha: p * 0.75,
        });
      });
    },
  },
  upnext: {
    dur: 8,
    draw(c, t, D) {
      const P = D.P,
        e = D.e.next;
      c.save();
      const k = 1.05 + 0.06 * pr(t, 0, 8);
      c.translate(960 + lerp(30, -30, pr(t, 0, 8)), 540);
      c.scale(k, k);
      c.translate(-960, -540);
      art(c, e.art, hashStr(e.t), 0, 0, W, H, t);
      c.restore();
      c.fillStyle = LG(c, 0, 0, W * 0.7, 0, [
        [0, hexA(P.bg, 0.96)],
        [0.55, hexA(P.bg, 0.75)],
        [1, hexA(P.bg, 0)],
      ]);
      c.fillRect(0, 0, W, H);
      c.fillStyle = LG(c, 0, H * 0.6, 0, H, [
        [0, hexA(P.bg, 0)],
        [1, hexA(P.bg, 0.8)],
      ]);
      c.fillRect(0, 0, W, H);
      const a1 = pr(t, 0.3, 1, E.outC);
      c.fillStyle = P.gold;
      c.fillRect(140, 292, 60 * a1, 2);
      T(c, "NEXT", 220, 302, { f: "Inter", w: 600, s: 28, c: P.gold, track: 10, alpha: a1 });
      const lines = wrap(c, e.t, 1000, { f: "Fraunces", w: 300, s: 132 });
      lines.forEach((ln, i) => {
        const p = pr(t, 0.6 + i * 0.12, 1.6 + i * 0.12, E.outQ);
        T(c, ln, 134, 440 + i * 140 + (1 - p) * 30, {
          f: "Fraunces",
          w: 300,
          s: 132,
          c: P.ivory,
          alpha: p,
        });
      });
      const y = 440 + (lines.length - 1) * 140 + 80;
      T(c, e.meta, 140, y, {
        f: "Inter",
        w: 500,
        s: 32,
        c: P.ivory,
        alpha: pr(t, 1.2, 2) * 0.7,
        track: 2,
      });
      const ll = wrap(c, e.log, 860, { f: "Fraunces", i: 1, w: 300, s: 42 });
      ll.forEach((ln, i) =>
        T(c, ln, 140, y + 80 + i * 58, {
          f: "Fraunces",
          i: 1,
          w: 300,
          s: 42,
          c: P.ivory,
          alpha: pr(t, 1.6 + i * 0.15, 2.4 + i * 0.15) * 0.9,
        }),
      );
      const cy = y + 80 + ll.length * 58 + 30,
        cp = pr(t, 2.6, 3.2, E.outC);
      if (cp > 0) {
        c.save();
        c.globalAlpha = cp;
        const tx = e.block.toUpperCase(),
          tw = meas(c, tx, { f: "Inter", w: 600, s: 22, track: 4 });
        c.strokeStyle = hexA(P.gold, 0.7);
        c.lineWidth = 2;
        rr(c, 140, cy, tw + 48, 50, 25);
        c.stroke();
        T(c, tx, 164, cy + 33, { f: "Inter", w: 600, s: 22, c: P.gold, track: 4 });
        c.restore();
      }
      T(c, `Begins at ${e.time}`, 140, 960, {
        f: "Inter",
        w: 500,
        s: 32,
        c: P.ivory,
        alpha: pr(t, 3, 3.6) * 0.85,
      });
      c.fillStyle = hexA(P.ivory, 0.15);
      c.fillRect(140, 995, 600, 2);
      c.fillStyle = P.gold;
      c.fillRect(140, 995, 600 * pr(t, 0, 8), 2);
    },
  },
  overlay: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e;
      programme(c, t, e.now.art, e.now.t);
      c.fillStyle = RG(c, 300, 1000, 0, 1000, [
        [0, "rgba(0,0,0,.7)"],
        [1, "rgba(0,0,0,0)"],
      ]);
      c.fillRect(0, 0, W, H);
      c.save();
      c.globalAlpha = 0.6;
      c.strokeStyle = P.gold;
      c.lineWidth = 2.5;
      c.beginPath();
      c.arc(1790, 120, 44, 0, TAU);
      c.stroke();
      T(c, "M", 1790, 122, { f: "Fraunces", w: 600, s: 44, a: "center", b: "middle", c: P.ivory });
      c.restore();
      const a = pr(t, 0.8, 1.6, E.outC) * (1 - pr(t, 5.6, 6.2));
      c.save();
      c.globalAlpha = a;
      c.fillStyle = P.gold;
      c.fillRect(140, 858, 90 * a, 2);
      T(c, "NOW PLAYING", 250, 866, { f: "Inter", w: 600, s: 22, c: P.gold, track: 8 });
      T(c, e.now.t, 140, 945, { f: "Fraunces", w: 300, s: 66, c: P.ivory });
      T(c, `Next at ${e.next.time}  ·  ${e.next.t}`, 142, 995, {
        f: "Inter",
        w: 500,
        s: 28,
        c: P.ivory,
        alpha: 0.7,
      });
      c.restore();
    },
  },
  break: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      mbg(c, P);
      beam(c, P, 0.06);
      dust(c, t, S, 60, P.gold);
      c.strokeStyle = hexA(P.ivory, 0.12);
      c.lineWidth = 3;
      c.beginPath();
      c.arc(960, 440, 130, 0, TAU);
      c.stroke();
      c.strokeStyle = P.gold;
      c.lineCap = "round";
      c.beginPath();
      c.arc(960, 440, 130, -Math.PI / 2, -Math.PI / 2 + TAU * pr(t, 0, 6));
      c.stroke();
      c.lineCap = "butt";
      star4(c, 960, 440, 46, t * 0.2, P.gold);
      T(c, "INTERMISSION", 960, 720, {
        f: "Fraunces",
        w: 300,
        s: 96,
        a: "center",
        c: P.ivory,
        track: lerp(40, 24, pr(t, 0, 2, E.outC)),
        alpha: pr(t, 0.2, 1),
      });
      T(c, D.e.breakNext, 960, 800, {
        f: "Fraunces",
        i: 1,
        w: 300,
        s: 40,
        a: "center",
        c: P.ivory,
        alpha: pr(t, 0.8, 1.6) * 0.7,
      });
    },
  },
  back: {
    dur: 4.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e.now;
      mbg(c, P);
      const lx = lerp(0, W, pr(t, 0.1, 1.3, E.io));
      c.save();
      c.beginPath();
      c.rect(0, 0, lx, H);
      c.clip();
      art(c, e.art, hashStr(e.t), 0, 0, W, H, t);
      c.fillStyle = hexA(P.bg, 0.55);
      c.fillRect(0, 0, W, H);
      c.restore();
      if (lx < W) {
        c.save();
        c.shadowColor = P.gold;
        c.shadowBlur = 30;
        c.fillStyle = P.gold;
        c.fillRect(lx - 2, 0, 4, H);
        c.restore();
      }
      T(c, "NOW RESUMING", 960, 450, {
        f: "Inter",
        w: 600,
        s: 28,
        a: "center",
        c: P.gold,
        track: 12,
        alpha: pr(t, 1.2, 1.8),
      });
      const p = pr(t, 1.4, 2.3, E.outQ);
      T(c, e.t, 960, 600 + (1 - p) * 30, {
        f: "Fraunces",
        w: 300,
        s: 124,
        a: "center",
        c: P.ivory,
        alpha: p,
      });
    },
  },
  offair: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      mbg(c, P);
      dust(c, t, S, 60, P.gold);
      const br = 1 + 0.03 * Math.sin(t * 1.4);
      c.save();
      c.translate(960, 400);
      c.scale(br, br);
      c.strokeStyle = hexA(P.gold, 0.8);
      c.lineWidth = 3;
      c.beginPath();
      c.arc(0, 0, 110, 0, TAU);
      c.stroke();
      star4(c, 0, 0, 48, t * 0.1, P.gold);
      c.restore();
      T(c, "MERIDIAN", 960, 680, {
        f: "Fraunces",
        w: 300,
        s: 80,
        a: "center",
        c: P.ivory,
        track: 26,
      });
      T(c, "resumes tomorrow at 7:00 PM", 960, 770, {
        f: "Fraunces",
        i: 1,
        w: 300,
        s: 44,
        a: "center",
        c: P.ivory,
        alpha: 0.75,
      });
      T(c, "TOMORROW  ·  HITCHCOCK IN COLOUR", 960, 850, {
        f: "Inter",
        w: 600,
        s: 24,
        a: "center",
        c: P.gold,
        track: 10,
      });
    },
  },
};

/* ============================ NEON 84 ============================== */
function synth(c, t, P, o = {}) {
  const hz = 640,
    sunY = o.sunY ?? 560,
    sunR = o.sunR || 300;
  const sky = LG(c, 0, 0, 0, hz, [
    [0, P.sky1],
    [1, P.sky2],
  ]);
  c.fillStyle = sky;
  c.fillRect(0, 0, W, hz);
  for (let i = 0; i < 70; i++) {
    c.fillStyle = `rgba(255,255,255,${(0.3 + 0.7 * rnd(9, i)) * (0.5 + 0.5 * Math.sin(t * 3 + i * 1.7))})`;
    c.fillRect(rnd(9, i + 100) * W, rnd(9, i + 200) * hz * 0.55, 3, 3);
  }
  c.save();
  c.beginPath();
  c.rect(0, 0, W, hz);
  c.clip();
  c.shadowColor = P.sun2;
  c.shadowBlur = 90;
  c.fillStyle = LG(c, 0, sunY - sunR, 0, sunY + sunR, [
    [0, P.sun1],
    [1, P.sun2],
  ]);
  c.beginPath();
  c.arc(960, sunY, sunR, 0, TAU);
  c.fill();
  c.shadowBlur = 0;
  c.fillStyle = sky;
  const gap = sunR * 0.16,
    sh = (t * 18) % gap;
  const top = Math.min(sunY, hz) - sunR * 0.55;
  for (let k = 0; k < 9; k++) {
    const yy = top + k * gap + sh;
    c.fillRect(960 - sunR - 10, yy, sunR * 2 + 20, 2 + clamp((yy - top) / (sunR * 1.2)) * 26);
  }
  c.restore();
  const pk = [
    [0, hz],
    [0, hz - 30],
    [180, hz - 120],
    [330, hz - 40],
    [470, hz - 190],
    [640, hz - 30],
    [720, hz],
    [1200, hz],
    [1300, hz - 60],
    [1450, hz - 210],
    [1580, hz - 70],
    [1720, hz - 150],
    [1920, hz - 20],
    [1920, hz],
  ];
  c.fillStyle = P.mount;
  c.beginPath();
  pk.forEach((p) => c.lineTo(p[0], p[1]));
  c.closePath();
  c.fill();
  c.strokeStyle = hexA(P.grid, 0.6);
  c.lineWidth = 2;
  c.stroke();
  c.fillStyle = LG(c, 0, hz, 0, H, [
    [0, P.ground],
    [1, "#000"],
  ]);
  c.fillRect(0, hz, W, H - hz);
  c.save();
  c.beginPath();
  c.rect(0, hz, W, H - hz);
  c.clip();
  c.strokeStyle = P.grid;
  c.shadowColor = P.grid;
  c.shadowBlur = 14;
  c.lineWidth = 3;
  const v = t * (o.speed ?? 1) * 1.5,
    fr = ((v % 1) + 1) % 1;
  for (let i = 0; i < 26; i++) {
    const z = i + 1 - fr,
      y = hz + 420 / z;
    if (y > H + 5) continue;
    c.globalAlpha = clamp(1.15 - z / 22);
    c.beginPath();
    c.moveTo(0, y);
    c.lineTo(W, y);
    c.stroke();
  }
  c.globalAlpha = 1;
  c.beginPath();
  for (let j = -18; j <= 18; j++) {
    c.moveTo(960 + j * 300 * (18 / (H - hz)), hz + 18);
    c.lineTo(960 + j * 300, H);
  }
  c.stroke();
  c.restore();
  c.fillStyle = LG(c, 0, hz - 6, 0, hz + 6, [
    [0, hexA(P.sun2, 0)],
    [0.5, hexA(P.sun2, 0.9)],
    [1, hexA(P.sun2, 0)],
  ]);
  c.fillRect(0, hz - 6, W, 12);
}
const chromeG = (c, y0, y1) =>
  LG(c, 0, y0, 0, y1, [
    [0, "#ffffff"],
    [0.42, "#a9c4ff"],
    [0.5, "#1d1446"],
    [0.54, "#ff9be0"],
    [0.8, "#ffe9fb"],
    [1, "#ffffff"],
  ]);
function chromeText(c, s, x, y, size, o = {}) {
  const a = o.a || "left",
    base = { f: "Orbitron", w: 900, s: size, a, alpha: o.alpha };
  T(c, s, x + 7, y + 9, { ...base, c: "rgba(20,0,40,.85)" });
  if (o.ch > 0) {
    T(c, s, x - o.ch, y, { ...base, c: "rgba(255,40,90,.75)" });
    T(c, s, x + o.ch, y, { ...base, c: "rgba(40,230,255,.75)" });
  }
  T(c, s, x, y, { ...base, c: chromeG(c, y - size * 0.76, y + size * 0.04), sw: 3, sc: "#ffffff" });
}
function osd(c, s, x, y, a = "left") {
  T(c, s, x + 4, y + 4, { f: "VT", s: 64, a, c: "rgba(0,0,0,.6)" });
  T(c, s, x, y, { f: "VT", s: 64, a, c: "#fff" });
}
function vhsBand(c, t, y, a) {
  const f = Math.floor(t * 30);
  c.save();
  c.globalAlpha = a;
  c.beginPath();
  c.rect(0, y, W, 130);
  c.clip();
  grain(c, t, 0.5, "source-over");
  c.fillStyle = "rgba(255,255,255,.35)";
  for (let i = 0; i < 14; i++)
    c.fillRect(
      rnd(f, i) * W * 0.5,
      y + rnd(f, i + 20) * 130,
      W * 0.3 + rnd(f, i + 40) * W * 0.7,
      2 + rnd(f, i + 60) * 6,
    );
  c.restore();
}
function cube(c, x, y, s, ax, ay, col) {
  const V = [];
  for (const a of [-1, 1]) for (const b of [-1, 1]) for (const d of [-1, 1]) V.push([a, b, d]);
  const Q = V.map(([X, Y, Z]) => {
    const y1 = Y * Math.cos(ax) - Z * Math.sin(ax),
      z1 = Y * Math.sin(ax) + Z * Math.cos(ax),
      x2 = X * Math.cos(ay) + z1 * Math.sin(ay),
      z2 = -X * Math.sin(ay) + z1 * Math.cos(ay),
      f = 3.2 / (z2 + 4);
    return [x + x2 * s * f, y + y1 * s * f];
  });
  c.save();
  c.strokeStyle = col;
  c.lineWidth = 5;
  c.shadowColor = col;
  c.shadowBlur = 26;
  c.beginPath();
  for (let i = 0; i < 8; i++)
    for (let j = i + 1; j < 8; j++) {
      let d = 0;
      for (let k = 0; k < 3; k++) if (V[i][k] !== V[j][k]) d++;
      if (d === 1) {
        c.moveTo(...Q[i]);
        c.lineTo(...Q[j]);
      }
    }
  c.stroke();
  c.restore();
}
const flick = (t, a, b, S) =>
  t < a ? 0 : t < b ? (rnd(S, Math.floor(t * 22)) > 0.42 ? 1 : 0.12) : 1;
const NEON = {
  ident: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      let sh = 0;
      if (t > 2.95 && t < 3.45) sh = 1 - pr(t, 2.95, 3.45);
      c.save();
      if (sh) {
        const f = Math.floor(t * 60);
        c.translate((rnd(S, f) - 0.5) * 44 * sh, (rnd(S, f + 7) - 0.5) * 44 * sh);
      }
      synth(c, t, P, { sunY: lerp(880, 560, pr(t, 0, 2.4, E.outC)) });
      const on = flick(t, 1.7, 2.5, S);
      if (on > 0)
        T(c, "NEON", 960, 330, {
          f: "Monoton",
          s: 220,
          a: "center",
          c: P.neon,
          glow: P.neon,
          gb: 44,
          alpha: on,
        });
      if (t > 2.6) {
        const sc = lerp(3.2, 1, pr(t, 2.6, 2.95, E.inC));
        c.save();
        c.translate(960, 520);
        c.scale(sc, sc);
        chromeText(c, "84", 0, 100, 260, { a: "center", ch: 3, alpha: pr(t, 2.6, 2.7) });
        c.restore();
      }
      const g = Math.sin(Math.PI * pr(t, 3.5, 4.1));
      if (g > 0) {
        c.save();
        c.shadowColor = "#fff";
        c.shadowBlur = 30;
        star4(c, 1095, 425, 70 * g, g, "#ffffff");
        c.restore();
      }
      TA(
        c,
        "TV LIKE YOU REMEMBER IT",
        960,
        760,
        {
          f: "VT",
          s: 60,
          c: "#fff",
          track: 6,
          a: "center",
          b: "alphabetic",
          sh: "rgba(0,0,0,.7)",
          shx: 4,
          shy: 4,
        },
        (i) => ({ al: t > 4 + i * 0.04 ? 1 : 0 }),
      );
      if (t < 0.7) vhsBand(c, t, lerp(H, -130, pr(t, 0, 0.7)), 1);
      c.restore();
    },
  },
  tonight: {
    dur: 9,
    draw(c, t, D) {
      const P = D.P,
        L = D.e.list;
      c.fillStyle = LG(c, 0, 0, 0, 430, [
        [0, P.sky1],
        [1, P.bar1],
      ]);
      c.fillRect(0, 0, W, 430);
      chromeText(c, "NEON 84", 120, 170, 110, { ch: 3 });
      T(c, "GUIDE", 128, 275, { f: "Monoton", s: 84, c: P.neon, glow: P.neon, gb: 24 });
      T(c, D.e.date, 130, 370, { f: "VT", s: 56, c: P.yellow });
      c.fillStyle = "#000";
      c.fillRect(1150, 50, 680, 340);
      art(c, L[0].art, hashStr(L[0].t), 1160, 60, 660, 320, t);
      c.strokeStyle = P.neon2;
      c.lineWidth = 4;
      c.strokeRect(1150, 50, 680, 340);
      c.fillStyle = "rgba(0,0,0,.6)";
      c.fillRect(1160, 330, 660, 50);
      T(c, "NOW: " + L[0].t.toUpperCase(), 1175, 367, { f: "VT", s: 44, c: "#fff" });
      c.save();
      c.beginPath();
      c.rect(0, 430, W, H - 430);
      c.clip();
      c.fillStyle = "#05052a";
      c.fillRect(0, 430, W, H - 430);
      const rh = 118,
        off = (t * 60) % (rh * L.length);
      for (let k = -1; k < L.length * 2 + 2; k++) {
        const it = L[((k % L.length) + L.length) % L.length],
          y = 440 + k * rh - off;
        if (y > H || y + rh < 430) continue;
        c.fillStyle = k % 2 ? P.bar1 : P.bar2;
        c.fillRect(40, y, W - 80, rh - 8);
        c.strokeStyle = hexA(P.neon2, 0.6);
        c.lineWidth = 2;
        c.strokeRect(40, y, W - 80, rh - 8);
        c.fillStyle = "#000";
        c.fillRect(60, y + 14, 260, rh - 36);
        T(c, it.time, 190, y + 76, { f: "VT", s: 64, a: "center", c: P.yellow });
        T(c, it.t.toUpperCase(), 360, y + 78, { f: "VT", s: 72, c: "#fff" });
        T(c, it.rating, 1840, y + 78, { f: "VT", s: 52, a: "right", c: P.neon2 });
      }
      c.restore();
    },
  },
  upnext: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed,
        e = D.e.next;
      synth(c, t, P, { sunY: 600, sunR: 220, speed: 0.6 });
      c.fillStyle = "rgba(0,0,0,.35)";
      c.fillRect(0, 0, W, H);
      osd(c, "PLAY ►", 100, 120);
      osd(c, `CH 84   ${D.e.clock}`, 1820, 120, "right");
      T(c, "UP NEXT", 140, 330, {
        f: "Monoton",
        s: 110,
        c: P.neon2,
        glow: P.neon2,
        gb: 34,
        alpha: flick(t, 0.3, 0.9, S),
      });
      const lines = wrap(c, e.t.toUpperCase(), 980, { f: "Orbitron", w: 900, s: 92 }),
        ch = lerp(46, 3, pr(t, 0.8, 1.5, E.outC)),
        ap = pr(t, 0.8, 1);
      lines.forEach((ln, i) => chromeText(c, ln, 140, 480 + i * 108, 92, { ch, alpha: ap }));
      const ty = 480 + (lines.length - 1) * 108 + 70;
      T(c, e.meta, 142, ty, { f: "VT", s: 50, c: "#fff", alpha: pr(t, 1.4, 1.8) });
      T(c, e.time, 140, ty + 180, {
        f: "VT",
        s: 170,
        c: P.neon,
        glow: P.neon,
        gb: 30,
        alpha: pr(t, 1.6, 2),
      });
      const bp = pr(t, 0.5, 1.2, E.back);
      c.save();
      c.translate(lerp(2300, 1460, bp), 560 + Math.sin(t * 1.6) * 12);
      c.rotate(0.08);
      c.fillStyle = "#0c0c10";
      rr(c, -270, -390, 540, 780, 16);
      c.fill();
      art(c, e.art, hashStr(e.t), -245, -330, 490, 560, t);
      c.fillStyle = P.neon;
      c.fillRect(-270, -390, 540, 58);
      T(c, "NEON 84 HOME VIDEO", 0, -348, { f: "VT", s: 42, a: "center", c: "#000" });
      c.fillStyle = "#f2f2f2";
      c.fillRect(-245, 250, 490, 110);
      T(c, e.t.toUpperCase(), 0, 298, { f: "Orbitron", w: 800, s: 26, a: "center", c: "#111" });
      T(c, e.box, 0, 342, { f: "VT", s: 34, a: "center", c: "#333" });
      c.restore();
    },
  },
  overlay: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e;
      programme(c, t, e.now.art, e.now.t);
      T(c, "84", 1800, 1010, {
        f: "Orbitron",
        w: 900,
        s: 64,
        a: "right",
        c: null,
        sw: 3,
        sc: P.neon,
        glow: P.neon,
        gb: 20,
        alpha: 0.8,
      });
      const dx = lerp(-1300, 0, pr(t, 0.8, 1.3, E.outC)) - pr(t, 5.6, 6.1, E.inC) * 1300;
      c.save();
      c.translate(dx, 0);
      c.fillStyle = P.neon;
      c.beginPath();
      c.moveTo(80, 850);
      c.lineTo(1180, 850);
      c.lineTo(1140, 950);
      c.lineTo(40, 950);
      c.closePath();
      c.fill();
      chromeText(c, "NOW: " + e.now.t.toUpperCase(), 110, 925, 58, { ch: 2 });
      c.fillStyle = P.bar1;
      c.beginPath();
      c.moveTo(60, 960);
      c.lineTo(940, 960);
      c.lineTo(920, 1020);
      c.lineTo(40, 1020);
      c.closePath();
      c.fill();
      T(c, `NEXT ${e.next.time} · ${e.next.t.toUpperCase()}`, 90, 1006, {
        f: "VT",
        s: 46,
        c: P.neon2,
      });
      c.restore();
    },
  },
  break: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        S = D.seed;
      synth(c, t, P, { sunY: 600, sunR: 240 });
      c.fillStyle = "rgba(0,0,0,.25)";
      c.fillRect(0, 0, W, H);
      cube(c, 960, 330, 120, t * 0.9, t * 0.6, P.neon2);
      const on = t < 1.2 ? flick(t, 0.4, 1.1, S) : t % 2.3 < 0.08 ? 0.3 : 1;
      T(c, "STAY TUNED", 960, 730, {
        f: "Monoton",
        s: 130,
        a: "center",
        c: P.neon,
        glow: P.neon,
        gb: 40,
        alpha: on,
      });
      T(c, D.e.breakNext, 964, 874, { f: "VT", s: 56, a: "center", c: "rgba(0,0,0,.7)" });
      T(c, D.e.breakNext, 960, 870, { f: "VT", s: 56, a: "center", c: "#fff" });
    },
  },
  back: {
    dur: 4.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e.now;
      if (t < 1.4) {
        synth(c, -t * 6, P, { sunY: 600, sunR: 240 });
        c.fillStyle = "rgba(0,0,0,.35)";
        c.fillRect(0, 0, W, H);
        for (let b = 0; b < 3; b++) vhsBand(c, t, ((t * 900 + b * 400) % (H + 200)) - 100, 0.8);
        osd(c, "◄◄ REW", 100, 120);
      } else {
        synth(c, t, P, { sunY: 600, sunR: 240 });
        osd(c, "PLAY ►", 100, 120);
        const k = t - 1.4;
        chromeText(c, "WE'RE BACK", 960, 480, 140, {
          a: "center",
          ch: lerp(40, 3, pr(k, 0, 0.6, E.outC)),
          alpha: pr(k, 0, 0.15),
        });
        T(c, e.t.toUpperCase(), 960, 660, {
          f: "Monoton",
          s: 96,
          a: "center",
          c: P.neon2,
          glow: P.neon2,
          gb: 30,
          alpha: pr(k, 0.5, 0.9),
        });
      }
    },
  },
  offair: {
    dur: 6,
    draw(c, t, D) {
      const cols = ["#c0c0c0", "#c0c000", "#00c0c0", "#00c000", "#c000c0", "#c00000", "#0000c0"],
        bw = W / 7,
        j = Math.floor(t * 30) % 17 === 0 ? 6 : 0;
      c.save();
      c.translate(0, j);
      cols.forEach((col, i) => {
        c.fillStyle = col;
        c.fillRect(i * bw, -10, bw + 1, H * 0.67 + 10);
      });
      ["#0000c0", "#131313", "#c000c0", "#131313", "#00c0c0", "#131313", "#c0c0c0"].forEach(
        (col, i) => {
          c.fillStyle = col;
          c.fillRect(i * bw, H * 0.67, bw + 1, H * 0.08);
        },
      );
      c.fillStyle = "#131313";
      c.fillRect(0, H * 0.75, W, H * 0.25 + 10);
      [
        ["#00214c", 0, 1.25],
        ["#ffffff", 1.25, 1.25],
        ["#32006a", 2.5, 1.25],
      ].forEach(([col, x, w]) => {
        c.fillStyle = col;
        c.fillRect(x * bw, H * 0.75, w * bw, H * 0.25 + 10);
      });
      c.restore();
      c.fillStyle = "rgba(0,0,0,.86)";
      rr(c, 440, 370, 1040, 280, 10);
      c.fill();
      T(c, "NEON 84 SIGNS ON AT 6:00 AM", 960, 490, { f: "VT", s: 76, a: "center", c: "#fff" });
      T(c, "1 kHz REFERENCE TONE", 960, 580, { f: "VT", s: 48, a: "center", c: "#ffe66d" });
    },
  },
};
function neonPost(c, t) {
  grain(c, t, 0.07);
  scanlines(c, 0.22);
  vignette(c, 0.45);
}

/* =========================== PERSONAL ============================== */
function aurora(c, t, P, dim = 1) {
  c.fillStyle = P.bg;
  c.fillRect(0, 0, W, H);
  c.save();
  c.globalCompositeOperation = "screen";
  [
    [P.h1, 0.5, 0.3, 0.18, 0.13, 820],
    [P.h2, 0.2, 0.72, 0.11, 0.17, 760],
    [P.h1 + 30, 0.85, 0.75, 0.15, 0.09, 700],
    [P.h2 - 30, 0.62, 0.12, 0.08, 0.14, 640],
  ].forEach(([h, x, y, fx, fy, r], i) => {
    const cx = W * (x + 0.12 * Math.sin(t * fx * 2 + i)),
      cy = H * (y + 0.1 * Math.cos(t * fy * 2 + i * 2));
    c.fillStyle = RG(c, cx, cy, 0, r, [
      [0, `hsla(${h},85%,55%,${0.5 * dim})`],
      [1, `hsla(${h},85%,55%,0)`],
    ]);
    c.fillRect(0, 0, W, H);
  });
  c.restore();
}
function glass(c, x, y, w, h, r) {
  rr(c, x, y, w, h, r);
  c.fillStyle = "rgba(255,255,255,.07)";
  c.fill();
  c.strokeStyle = "rgba(255,255,255,.16)";
  c.lineWidth = 2;
  c.stroke();
}
function mono(c, x, y, s, P, ch) {
  c.save();
  c.shadowColor = `hsla(${P.h1},90%,50%,.5)`;
  c.shadowBlur = s * 0.3;
  c.fillStyle = LG(c, x - s / 2, y - s / 2, x + s / 2, y + s / 2, [
    [0, P.a],
    [1, P.b],
  ]);
  rr(c, x - s / 2, y - s / 2, s, s, s * 0.28);
  c.fill();
  c.restore();
  c.save();
  rr(c, x - s / 2, y - s / 2, s, s, s * 0.28);
  c.clip();
  c.fillStyle = "rgba(255,255,255,.18)";
  c.beginPath();
  c.ellipse(x - s * 0.2, y - s * 0.45, s * 0.6, s * 0.3, -0.3, 0, TAU);
  c.fill();
  c.restore();
  T(c, ch, x, y + s * 0.04, {
    f: "Grotesk",
    w: 700,
    s: s * 0.62,
    a: "center",
    b: "middle",
    c: "#fff",
  });
}
const PERS = {
  ident: {
    dur: 7,
    draw(c, t, D) {
      const P = D.P,
        n = D.name;
      aurora(c, t, P);
      let ns = 150;
      const w150 = meas(c, n, { f: "Grotesk", w: 700, s: 150 });
      if (w150 > 1150) ns = (150 * 1150) / w150;
      const nameW = meas(c, n, { f: "Grotesk", w: 700, s: ns }),
        pillW = 130,
        ms = 190,
        total = ms + 44 + nameW + 26 + pillW,
        x0 = 960 - total / 2;
      const mp = pr(t, 0.2, 1, E.back),
        mx = lerp(960, x0 + ms / 2, pr(t, 1.3, 2.1, E.io));
      c.save();
      c.translate(mx, 450);
      c.scale(mp, mp);
      mono(c, 0, 0, ms, P, n[0].toUpperCase());
      c.restore();
      const nx = x0 + ms + 44;
      c.save();
      c.beginPath();
      c.rect(nx - 10, 300, lerp(0, nameW + 40, pr(t, 1.7, 2.5, E.io)), 300);
      c.clip();
      T(c, n, nx, 450, { f: "Grotesk", w: 700, s: ns, b: "middle", c: "#fff" });
      c.restore();
      const pp = pr(t, 2.4, 2.9, E.back);
      if (pp > 0) {
        c.save();
        c.translate(nx + nameW + 26 + pillW / 2, 450);
        c.scale(pp, pp);
        c.fillStyle = LG(c, -pillW / 2, 0, pillW / 2, 0, [
          [0, P.a],
          [1, P.b],
        ]);
        rr(c, -pillW / 2, -48, pillW, 96, 48);
        c.fill();
        T(c, "TV", 0, 4, { f: "Grotesk", w: 700, s: 60, a: "center", b: "middle", c: P.bg });
        c.restore();
      }
      const g = pr(t, 2.9, 3.6, E.outC);
      T(c, `${D.greet}, ${n}.`, 960, 700 + (1 - g) * 30, {
        f: "Grotesk",
        w: 500,
        s: 64,
        a: "center",
        c: "#fff",
        alpha: g,
      });
      const g2 = pr(t, 3.4, 4.1, E.outC);
      T(
        c,
        "This channel is made for you. Nobody else gets this line-up.",
        960,
        770 + (1 - g2) * 30,
        { f: "Inter", s: 38, a: "center", c: "#fff", alpha: g2 * 0.65 },
      );
      const chs = D.e.taste,
        cw = chs.map((s) => meas(c, s, { f: "Inter", w: 500, s: 32 }) + 64),
        tw = cw.reduce((a, b) => a + b, 0) + 24 * (chs.length - 1);
      let x = 960 - tw / 2;
      chs.forEach((s, i) => {
        const p = pr(t, 4.1 + i * 0.15, 4.6 + i * 0.15, E.back);
        if (p > 0) {
          c.save();
          c.translate(x + cw[i] / 2, 900);
          c.scale(p, p);
          glass(c, -cw[i] / 2, -34, cw[i], 68, 34);
          T(c, s, 0, 2, { f: "Inter", w: 500, s: 32, a: "center", b: "middle", c: "#fff" });
          c.restore();
        }
        x += cw[i] + 24;
      });
    },
  },
  tonight: {
    dur: 8,
    draw(c, t, D) {
      const P = D.P,
        L = D.e.list;
      aurora(c, t, P, 0.7);
      T(c, `Your night, ${D.name}`, 140, 200, {
        f: "Grotesk",
        w: 700,
        s: 88,
        c: "#fff",
        alpha: pr(t, 0.1, 0.7),
      });
      T(c, D.e.built, 142, 262, { f: "Inter", s: 32, c: "#fff", alpha: pr(t, 0.4, 1) * 0.6 });
      c.fillStyle = "rgba(255,255,255,.18)";
      c.fillRect(329, 350, 2, 640 * pr(t, 0.5, 1.6, E.io));
      L.forEach((it, i) => {
        const y = 410 + i * 165,
          p = pr(t, 0.7 + i * 0.22, 1.4 + i * 0.22, E.outC);
        c.save();
        c.globalAlpha = p;
        c.translate((1 - p) * 60, 0);
        T(c, it.time, 290, y + 12, {
          f: "Inter",
          w: 600,
          s: 32,
          a: "right",
          c: "#fff",
          alpha: 0.8,
        });
        c.fillStyle = i === 0 ? P.a : "rgba(255,255,255,.5)";
        c.beginPath();
        c.arc(330, y, i === 0 ? 12 : 8, 0, TAU);
        c.fill();
        if (i === 0) {
          const pl = (t * 0.9) % 1;
          c.strokeStyle = `hsla(${P.h1},90%,66%,${1 - pl})`;
          c.lineWidth = 3;
          c.beginPath();
          c.arc(330, y, 12 + pl * 26, 0, TAU);
          c.stroke();
        }
        glass(c, 380, y - 66, 1400, 132, 30);
        const tw = meas(c, it.tag, { f: "Inter", w: 700, s: 20, track: 3 }) + 28;
        c.fillStyle = i === 0 ? P.a : `hsla(${i % 2 ? P.h2 : P.h1},90%,66%,.22)`;
        rr(c, 414, y - 48, tw, 34, 17);
        c.fill();
        T(c, it.tag, 428, y - 24, {
          f: "Inter",
          w: 700,
          s: 20,
          track: 3,
          c: i === 0 ? P.bg : "#fff",
        });
        T(c, it.t, 414, y + 36, { f: "Grotesk", w: 700, s: 46, c: "#fff" });
        T(c, it.why, 1748, y + 12, { f: "Inter", s: 30, a: "right", c: "#fff", alpha: 0.65 });
        if (it.progress) {
          c.fillStyle = "rgba(255,255,255,.14)";
          rr(c, 414, y + 50, 700, 6, 3);
          c.fill();
          c.fillStyle = P.a;
          rr(c, 414, y + 50, 700 * it.progress, 6, 3);
          c.fill();
        }
        c.restore();
      });
    },
  },
  upnext: {
    dur: 7.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e.next;
      aurora(c, t, P, 0.8);
      const cp = pr(t, 0, 0.6, E.outC);
      c.save();
      c.globalAlpha = cp;
      c.translate(0, (1 - cp) * 40);
      glass(c, 140, 150, 1640, 780, 48);
      c.save();
      rr(c, 190, 200, 440, 680, 30);
      c.clip();
      const k = 1.04 + 0.04 * pr(t, 0, 7.5);
      c.translate(410, 540);
      c.scale(k, k);
      c.translate(-410, -540);
      art(c, e.art, hashStr(e.t), 190, 200, 440, 680, t);
      c.restore();
      T(c, "UP NEXT FOR YOU", 700, 290, {
        f: "Inter",
        w: 600,
        s: 28,
        c: P.a,
        track: 6,
        alpha: pr(t, 0.3, 0.8),
      });
      T(c, e.t, 696, 410, { f: "Grotesk", w: 700, s: 120, c: "#fff", alpha: pr(t, 0.4, 1) });
      T(c, e.meta, 700, 480, {
        f: "Inter",
        w: 500,
        s: 34,
        c: "#fff",
        alpha: pr(t, 0.6, 1.2) * 0.65,
      });
      const rp = pr(t, 1, 1.6, E.outC);
      c.save();
      c.globalAlpha *= rp;
      c.translate((1 - rp) * -20, 0);
      heart(c, 722, 598, 20, P.b);
      T(c, e.reason, 764, 610, { f: "Inter", w: 500, s: 38, c: "#fff" });
      c.restore();
      const m = Math.round(e.match * pr(t, 1.2, 2.6, E.outC));
      c.lineWidth = 14;
      c.strokeStyle = "rgba(255,255,255,.1)";
      c.beginPath();
      c.arc(1580, 330, 90, 0, TAU);
      c.stroke();
      if (m > 0) {
        c.strokeStyle = LG(c, 1490, 240, 1670, 420, [
          [0, P.a],
          [1, P.b],
        ]);
        c.lineCap = "round";
        c.beginPath();
        c.arc(1580, 330, 90, -Math.PI / 2, -Math.PI / 2 + (TAU * m) / 100);
        c.stroke();
        c.lineCap = "butt";
      }
      T(c, m + "%", 1580, 345, { f: "Grotesk", w: 700, s: 56, a: "center", c: "#fff" });
      T(c, "match", 1580, 385, { f: "Inter", w: 500, s: 24, a: "center", c: "#fff", alpha: 0.6 });
      T(c, e.when, 700, 800, {
        f: "Inter",
        w: 500,
        s: 36,
        c: "#fff",
        alpha: pr(t, 1.8, 2.4) * 0.85,
      });
      c.fillStyle = "rgba(255,255,255,.12)";
      rr(c, 700, 830, 1000, 8, 4);
      c.fill();
      c.fillStyle = LG(c, 700, 0, 1700, 0, [
        [0, P.a],
        [1, P.b],
      ]);
      rr(c, 700, 830, Math.max(8, 1000 * pr(t, 0, 7.5)), 8, 4);
      c.fill();
      c.restore();
    },
  },
  overlay: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e,
        ini = D.name[0].toUpperCase();
      programme(c, t, e.now.art, e.now.t);
      c.save();
      c.globalAlpha = 0.85;
      mono(c, 1800, 1000, 64, P, ini);
      c.restore();
      const a = pr(t, 0.8, 1.4, E.outC) * (1 - pr(t, 5.6, 6.1));
      c.save();
      c.globalAlpha = a;
      c.translate(0, (1 - a) * 30);
      rr(c, 120, 860, 1100, 130, 65);
      c.fillStyle = "rgba(10,10,14,.6)";
      c.fill();
      c.strokeStyle = "rgba(255,255,255,.16)";
      c.lineWidth = 2;
      c.stroke();
      mono(c, 190, 925, 84, P, ini);
      T(c, "NOW", 258, 906, { f: "Inter", w: 700, s: 20, c: P.a, track: 4 });
      T(c, e.now.t, 258, 952, { f: "Grotesk", w: 700, s: 44, c: "#fff" });
      T(c, `Next for you ${e.next.time} · ${e.next.t}`, 1190, 950, {
        f: "Inter",
        w: 500,
        s: 28,
        a: "right",
        c: "#fff",
        alpha: 0.7,
      });
      c.restore();
    },
  },
  break: {
    dur: 6.5,
    draw(c, t, D) {
      const P = D.P,
        e = D.e.promo;
      aurora(c, t, P, 0.6);
      T(c, "Back in a moment", 140, 200, {
        f: "Grotesk",
        w: 700,
        s: 84,
        c: "#fff",
        alpha: pr(t, 0.1, 0.6),
      });
      T(c, D.e.breakNext, 142, 262, {
        f: "Inter",
        s: 34,
        c: "#fff",
        alpha: pr(t, 0.3, 0.9) * 0.65,
      });
      const left = 118 - Math.floor(t),
        lbl = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
      c.lineWidth = 6;
      c.strokeStyle = "rgba(255,255,255,.12)";
      c.beginPath();
      c.arc(1700, 200, 54, 0, TAU);
      c.stroke();
      c.strokeStyle = P.a;
      c.beginPath();
      c.arc(1700, 200, 54, -Math.PI / 2, -Math.PI / 2 + TAU * (1 - t / 120));
      c.stroke();
      T(c, lbl, 1700, 212, { f: "Grotesk", w: 700, s: 34, a: "center", c: "#fff" });
      const vp = pr(t, 0.3, 1, E.outC);
      c.save();
      c.globalAlpha = vp;
      c.translate(0, (1 - vp) * 40);
      c.save();
      rr(c, 140, 340, 1060, 600, 36);
      c.clip();
      const k = 1.05 + 0.1 * pr(t, 0, 6.5);
      c.translate(670, 640);
      c.scale(k, k);
      c.translate(-670, -640);
      art(c, e.art, hashStr(e.t), 140, 340, 1060, 600, t);
      c.restore();
      c.strokeStyle = "rgba(255,255,255,.18)";
      c.lineWidth = 2;
      rr(c, 140, 340, 1060, 600, 36);
      c.stroke();
      c.fillStyle = "rgba(0,0,0,.45)";
      rr(c, 170, 370, 150, 44, 22);
      c.fill();
      T(c, "TRAILER", 245, 400, { f: "Inter", w: 700, s: 20, a: "center", c: "#fff", track: 4 });
      c.fillStyle = "rgba(255,255,255,.25)";
      c.fillRect(180, 900, 980, 5);
      c.fillStyle = "#fff";
      c.fillRect(180, 900, 980 * pr(t, 0, 6.5), 5);
      c.restore();
      const r = pr(t, 0.8, 1.4, E.outC);
      c.save();
      c.globalAlpha = r;
      c.translate((1 - r) * 40, 0);
      T(c, "FROM YOUR WATCHLIST", 1270, 450, { f: "Inter", w: 600, s: 26, c: P.a, track: 6 });
      const ln = wrap(c, e.t, 520, { f: "Grotesk", w: 700, s: 80 });
      ln.forEach((s, i) => T(c, s, 1266, 550 + i * 88, { f: "Grotesk", w: 700, s: 80, c: "#fff" }));
      const y = 550 + (ln.length - 1) * 88 + 70;
      T(c, e.when, 1270, y, { f: "Inter", w: 500, s: 32, c: "#fff", alpha: 0.8 });
      const pw = meas(c, "★  On your watchlist", { f: "Inter", w: 600, s: 28 }) + 48;
      glass(c, 1270, y + 40, pw, 60, 30);
      T(c, "★  On your watchlist", 1294, y + 80, { f: "Inter", w: 600, s: 28, c: "#fff" });
      c.restore();
    },
  },
  welcome: {
    dur: 6,
    draw(c, t, D) {
      const P = D.P,
        n = D.name,
        e = D.e.resume;
      aurora(c, t, P);
      mono(c, 190, 150, 90, P, n[0].toUpperCase());
      T(c, n, 262, 168, { f: "Grotesk", w: 700, s: 48, c: "#fff" });
      const a = pr(t, 0.2, 0.9, E.outC);
      T(c, `Welcome back, ${n}.`, 960, 400 + (1 - a) * 30, {
        f: "Grotesk",
        w: 700,
        s: 104,
        a: "center",
        c: "#fff",
        alpha: a,
      });
      const b = pr(t, 0.7, 1.4, E.outC);
      T(c, e.line, 960, 480 + (1 - b) * 20, {
        f: "Inter",
        s: 42,
        a: "center",
        c: "#fff",
        alpha: b * 0.75,
      });
      const cp = pr(t, 1.2, 1.9, E.outC);
      c.save();
      c.globalAlpha = cp;
      c.translate(0, (1 - cp) * 40);
      glass(c, 440, 570, 1040, 280, 36);
      c.save();
      rr(c, 470, 600, 400, 220, 22);
      c.clip();
      art(c, e.art, hashStr(e.t), 470, 600, 400, 220, t);
      c.restore();
      T(c, e.t, 910, 680, { f: "Grotesk", w: 700, s: 54, c: "#fff" });
      T(c, e.sub, 912, 730, { f: "Inter", w: 500, s: 32, c: "#fff", alpha: 0.65 });
      c.fillStyle = "rgba(255,255,255,.14)";
      rr(c, 912, 770, 520, 8, 4);
      c.fill();
      c.fillStyle = LG(c, 912, 0, 1432, 0, [
        [0, P.a],
        [1, P.b],
      ]);
      rr(c, 912, 770, Math.max(8, 520 * e.progress * pr(t, 1.4, 2.4, E.outC)), 8, 4);
      c.fill();
      c.restore();
      const rp = pr(t, 2.4, 2.8);
      T(c, `Resuming in ${Math.max(1, Math.ceil(5.6 - t))}…`, 960, 930, {
        f: "Inter",
        w: 600,
        s: 36,
        a: "center",
        c: P.a,
        alpha: rp,
      });
      T(c, "Your channel paused while you were away.", 960, 990, {
        f: "Inter",
        s: 28,
        a: "center",
        c: "#fff",
        alpha: rp * 0.5,
      });
    },
  },
};

/* ============================ brands =============================== */
const BR = {
  blorp: {
    label: "BLORP!",
    dot: "linear-gradient(135deg,#6B2BD9,#FF7A00 60%,#7CE03A)",
    scenes: KIDS,
    post: null,
    type: "Bungee + Fredoka",
    motion: "squash & stretch, overshoot, wobble",
    about:
      "<b>Kids network.</b> An original brand in the spirit of 90s kids TV: goo, googly eyes, squash and stretch. The mascot, drips and confetti come from seeded shapes, so every channel built from this family looks different.",
    palettes: [
      { bg: "#6B2BD9", a: "#FF7A00", b: "#7CE03A", c: "#FF3E8A", d: "#FFD23F", ink: "#1B0B3A" },
      { bg: "#0FA3B1", a: "#FF5A1F", b: "#B4F000", c: "#FF2E93", d: "#FFE14D", ink: "#08262B" },
      { bg: "#E8337E", a: "#FFB800", b: "#2BD96B", c: "#6B2BD9", d: "#FFF15C", ink: "#2A0A1F" },
    ],
    segs: ["flow", "ident", "tonight", "upnext", "break", "back", "overlay", "offair"],
    flow: ["ident", "tonight", "upnext", "overlay", "break", "back"],
    e: {
      now: { t: "Bluey", art: "toon" },
      next: {
        t: "Avatar: The Last Airbender",
        sub: "S1 · E4  The Warriors of Kyoshi",
        art: "toon",
        time: "4:30",
      },
      list: [
        { time: "4:00", t: "Bluey", sub: "Magic Xylophone" },
        { time: "4:30", t: "Avatar: The Last Airbender", sub: "The Warriors of Kyoshi" },
        { time: "5:00", t: "Gravity Falls", sub: "Tourist Trapped" },
        { time: "5:30", t: "Adventure Time", sub: "Slumber Party Panic" },
      ],
      breakNext: "After the break: more Bluey!",
      offair: "Back at 6:00 AM with Bluey",
    },
  },
  nocturne: {
    label: "NOCTURNE",
    dot: "linear-gradient(135deg,#060508,#C8102E)",
    scenes: NOCT,
    post: noirPost,
    type: "Playfair Display + Special Elite",
    motion: "slow, flicker, typewriter, grain",
    about:
      "<b>Late-night horror.</b> Moonrise, bats, typewriter titles, film grain and projector flicker. The break is a door creaking open; the return is a film-leader countdown.",
    palettes: [
      { bg: "#060508", red: "#C8102E", bone: "#EDE6D6", fog: "#8FA3B8", moon: "#E9E3D1" },
      { bg: "#050806", red: "#56D27A", bone: "#E3EAD8", fog: "#7FA08A", moon: "#E3EDD8" },
      { bg: "#08060a", red: "#E8A33A", bone: "#F1E8D8", fog: "#A89A8A", moon: "#F3E6C8" },
    ],
    segs: ["flow", "ident", "tonight", "upnext", "break", "back", "overlay", "offair"],
    flow: ["ident", "tonight", "upnext", "overlay", "break", "back"],
    e: {
      now: { t: "Carnival of Souls", year: "1962", art: "night" },
      next: {
        t: "Night of the Living Dead",
        art: "night",
        meta: "1968  ·  GEORGE A. ROMERO  ·  1H 36M",
        log: "Seven strangers, one farmhouse, and a night that refuses to end.",
        time: "MIDNIGHT",
      },
      list: [
        { time: "10:30 PM", t: "Carnival of Souls", year: "1962" },
        { time: "12:00 AM", t: "Night of the Living Dead", year: "1968" },
        { time: "1:40 AM", t: "Nosferatu", year: "1922" },
        { time: "3:15 AM", t: "House on Haunted Hill", year: "1959" },
      ],
      nextLine: "NEXT 12:00 AM — NIGHT OF THE LIVING DEAD",
      breakNext: "CARNIVAL OF SOULS RETURNS AFTER THE BREAK",
      offair: "Returns at 10:30 PM with Carnival of Souls",
    },
  },
  meridian: {
    label: "MERIDIAN",
    dot: "linear-gradient(135deg,#070d1d,#17254a 55%,#D8B26E)",
    scenes: MER,
    post: (c, t) => {
      grain(c, t, 0.06);
      vignette(c, 0.5);
    },
    type: "Fraunces + Inter",
    motion: "slow eases, line draws, light sweeps",
    about:
      '<b>Prestige movie channel.</b> Slow gold line work, a light sweep across the wordmark, and serif titles over the film\'s backdrop. The AI writes the logline and names the block ("Films noir of the forties").',
    palettes: [
      { bg: "#070d1d", bg2: "#17254a", gold: "#D8B26E", ivory: "#F4EEDF" },
      { bg: "#140809", bg2: "#3a1418", gold: "#D9A86A", ivory: "#F6EDE4" },
      { bg: "#06110d", bg2: "#15302a", gold: "#CDB27A", ivory: "#EEF0E6" },
    ],
    segs: ["flow", "ident", "tonight", "upnext", "break", "back", "overlay", "offair"],
    flow: ["ident", "tonight", "upnext", "overlay", "break", "back"],
    e: {
      now: { t: "Double Indemnity", art: "noir" },
      next: {
        t: "The Third Man",
        art: "city",
        meta: "1949  ·  Carol Reed  ·  1h 44m  ·  PG",
        log: "A pulp novelist arrives in postwar Vienna to bury his oldest friend, and finds the story has already been rewritten.",
        time: "9:00 PM",
        block: "Thursday · Films noir of the forties",
      },
      list: [
        { time: "7:00", t: "Double Indemnity", year: "1944", art: "noir" },
        { time: "9:00", t: "The Third Man", year: "1949", art: "city" },
        { time: "10:45", t: "Sunset Boulevard", year: "1950", art: "dusk" },
        { time: "12:45", t: "The Big Sleep", year: "1946", art: "noir" },
      ],
      blockName: "Thursday Noir",
      breakNext: "Double Indemnity continues in a moment.",
    },
  },
  neon: {
    label: "NEON 84",
    dot: "linear-gradient(135deg,#12002b,#ff2e88 60%,#22e6ff)",
    scenes: NEON,
    post: neonPost,
    type: "Monoton + Orbitron + VT323",
    motion: "slam-ins, flicker, chroma split, VHS tracking",
    about:
      "<b>80s retro.</b> Synthwave sun, scrolling grid, chrome type, VHS on-screen display, a scrolling guide channel, and colour bars when off air.",
    palettes: [
      {
        sky1: "#12002b",
        sky2: "#ff2e88",
        sun1: "#ffe66d",
        sun2: "#ff2e88",
        grid: "#22e6ff",
        mount: "#1a0533",
        ground: "#14002e",
        neon: "#ff4fd8",
        neon2: "#22e6ff",
        bar1: "#1b1f6b",
        bar2: "#2a2f94",
        yellow: "#ffe66d",
      },
      {
        sky1: "#001a2e",
        sky2: "#00b3ff",
        sun1: "#fff3a8",
        sun2: "#ff7a00",
        grid: "#ff4fd8",
        mount: "#001226",
        ground: "#000c1a",
        neon: "#00e1ff",
        neon2: "#ff4fd8",
        bar1: "#002a55",
        bar2: "#003a75",
        yellow: "#ffd23f",
      },
      {
        sky1: "#1a0010",
        sky2: "#ff5f2e",
        sun1: "#fff0a0",
        sun2: "#ff2e5f",
        grid: "#b14dff",
        mount: "#24001a",
        ground: "#12000c",
        neon: "#ffd23f",
        neon2: "#b14dff",
        bar1: "#3a0a4a",
        bar2: "#4f1166",
        yellow: "#ffd23f",
      },
    ],
    segs: ["flow", "ident", "tonight", "upnext", "break", "back", "overlay", "offair"],
    flow: ["ident", "tonight", "upnext", "overlay", "break", "back"],
    e: {
      now: { t: "Ghostbusters", art: "city" },
      next: {
        t: "Back to the Future",
        art: "dusk",
        meta: "1985 · PG · 116 MIN",
        time: "9:45 PM",
        box: "PG · 116 MIN · HI-FI STEREO",
      },
      clock: "9:44 PM",
      date: "THU NOV 4 · 9:44 PM",
      list: [
        { time: "8:00", t: "Ghostbusters", rating: "PG", art: "city" },
        { time: "9:45", t: "Back to the Future", rating: "PG" },
        { time: "11:45", t: "The Terminator", rating: "R" },
        { time: "1:45", t: "Tron", rating: "PG" },
        { time: "3:30", t: "The Goonies", rating: "PG" },
      ],
      breakNext: "MORE GHOSTBUSTERS AFTER THESE MESSAGES",
    },
  },
  personal: {
    label: "Your TV",
    dot: "linear-gradient(135deg,#6d5cff,#ff5ca8)",
    scenes: PERS,
    post: (c, t) => grain(c, t, 0.05),
    type: "Space Grotesk + Inter",
    motion: "fluid, springy, glass",
    about:
      "<b>A personal channel for each viewer.</b> The palette comes from the viewer's name. The line-up comes from their taste model, Continue Watching and watchlist, and the breaks play trailers from their own watchlist. In on-demand mode it pauses when they leave and greets them when they tune back in.",
    palette: (st) => {
      const h = (hashStr((st.name || "You").toLowerCase()) + st.seed * 47) % 360,
        h2 = (h + 50) % 360;
      return { h1: h, h2, bg: `hsl(${h},38%,6%)`, a: `hsl(${h},92%,66%)`, b: `hsl(${h2},92%,66%)` };
    },
    segs: ["flow", "welcome", "ident", "tonight", "upnext", "break", "overlay"],
    flow: ["welcome", "overlay", "break", "upnext", "tonight", "ident"],
    e: {
      taste: ["Slow-burn sci-fi", "Workplace satire", "Heist movies", "Comfort reruns"],
      now: { t: "Severance · S2 E7", art: "city" },
      next: {
        t: "Arrival",
        art: "shell",
        meta: "2016  ·  Denis Villeneuve  ·  1h 56m",
        reason: "Because you rated Annihilation 5★",
        match: 96,
        when: "Starts 9:41 PM · right after Severance",
        time: "9:41 PM",
      },
      list: [
        {
          time: "8:52",
          tag: "CONTINUE",
          t: "Severance · S2 E7",
          why: "18 min left",
          progress: 0.62,
        },
        {
          time: "9:41",
          tag: "PICKED FOR YOU",
          t: "Arrival",
          why: "Because you rated Annihilation 5★",
        },
        {
          time: "11:40",
          tag: "HEIST NIGHT",
          t: "Ocean's Eleven",
          why: "Every Friday on your channel",
        },
        {
          time: "1:45",
          tag: "COMFORT RERUN",
          t: "The Office · S5 E21",
          why: "You’ve rewatched this 4 times",
        },
      ],
      built: "Built from 412 things you’ve watched. Changes as you watch.",
      promo: { t: "Dune: Part Two", art: "desert", when: "Plays Friday 9:00 PM on your channel" },
      breakNext: "Severance continues after this.",
      resume: {
        t: "Severance",
        sub: "S2 E7 · Chikhai Bardo",
        art: "city",
        progress: 0.62,
        line: "You were 31 minutes into Severance.",
      },
    },
  },
};

export const referenceBrands = BR;
const logos = new Map();
export function renderLook(
  c: CanvasRenderingContext2D,
  look: Look,
  moment: Moment,
  bindings: Bindings = {},
  t = 3,
) {
  if (!c) return;
  c.__look = look;
  const key =
    { kids: "blorp", lateNight: "nocturne", cinema: "meridian", retro: "neon", clean: "meridian" }[
      look.family
    ] || look.family;
  const b = BR[key] || BR.meridian;
  const seed = look.brand.seed;
  const name = bindings.viewer?.name || "Braedon";
  const P = { ...(b.palette ? b.palette({ name, seed }) : b.palettes[seed % b.palettes.length]) };
  const p = look.brand.palette;
  Object.assign(P, {
    bg: p.background,
    a: p.accent,
    b: p.glow,
    red: p.accent,
    bone: p.text,
    gold: p.accent,
    ivory: p.text,
    neon: p.accent,
    neon2: p.glow,
    grid: p.glow,
    sky1: p.background,
  });
  const e = structuredClone(b.e);
  if (bindings.now) e.now = { ...e.now, t: bindings.now.title };
  if (bindings.next)
    e.next = {
      ...e.next,
      t: bindings.next.title,
      time: bindings.next.time || e.next.time,
      reason: bindings.next.reason || e.next.reason,
    };
  if (bindings.list)
    e.list = bindings.list.map((v, i) => ({
      ...b.e.list[i % b.e.list.length],
      t: v.title,
      time: v.time,
      why: v.reason || "",
    }));
  let k = moment === "bug" || moment === "lowerthird" ? "overlay" : moment;
  if (!b.scenes[k]) k = "ident";
  const D = { P, seed: seed * 1000 + 7, e, name, greet: bindings.greeting || "Good evening" };
  c.setTransform(c.canvas.width / W, 0, 0, c.canvas.height / H, 0, 0);
  c.globalAlpha = 1;
  c.globalCompositeOperation = "source-over";
  c.shadowBlur = 0;
  c.filter = "none";
  c.clearRect(0, 0, W, H);
  c.save();
  b.scenes[k].draw(c, t * (0.6 + look.brand.energy * 0.8), D);
  c.restore();
  if (look.brand.logo) {
    c.save();
    c.fillStyle = p.accent;
    c.font = `700 72px ${look.brand.type.display}`;
    if (look.brand.logo.length < 4) c.fillText(look.brand.logo, 95, 130);
    else {
      let image = logos.get(look.brand.logo);
      if (!image && typeof Image !== "undefined") {
        image = new Image();
        image.src = look.brand.logo;
        logos.set(look.brand.logo, image);
      }
      if (image?.complete && image.naturalWidth) c.drawImage(image, 95, 70, 120, 120);
    }
    c.restore();
  }
  if (b.post && look.brand.textures.length) {
    c.save();
    b.post(c, t, D, k);
    c.restore();
  }
}
let fontPromise: Promise<void> | undefined;
export function loadOnAirFonts() {
  return (fontPromise ??= (async () => {
    const names = [
      "Bungee",
      "Fredoka",
      "Special Elite",
      "Playfair Display",
      "Fraunces",
      "Inter",
      "Monoton",
      "Orbitron",
      "VT323",
      "Space Grotesk",
    ];
    await Promise.all(
      names.map(async (name) => {
        try {
          const css = await (
            await fetch(
              "https://fonts.googleapis.com/css2?family=" +
                encodeURIComponent(name) +
                ":wght@400;700&display=swap",
            )
          ).text();
          const blocks = [...css.matchAll(/@font-face\s*\{([^}]+)\}/g)];
          await Promise.all(
            blocks.map(async ([, block]) => {
              const src = block.match(/src:\s*([^;]+)/)?.[1];
              if (!src) return;
              const face = new FontFace(name, src, {
                weight: block.match(/font-weight:\s*([^;]+)/)?.[1] || "400",
              });
              await face.load();
              document.fonts.add(face);
            }),
          );
        } catch {
          /* local fallback remains usable offline */
        }
      }),
    );
  })());
}
