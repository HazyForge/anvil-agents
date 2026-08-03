import { useEffect, useRef } from "react";

/**
 * Cinematic cluster capacity field — the product "video" background.
 * Evolves the hazyforge.io HeroForge wave field into hunter-green job
 * scheduling geometry: perspective contours, pulse nodes, and job sparks.
 */
export default function ClusterFieldBackground() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const reduceMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;

    let disposed = false;
    let raf = 0;
    const mouse = { x: 0.5, y: 0.5, tx: 0.5, ty: 0.5 };
    const dims = { w: 0, h: 0 };
    const rect = { left: 0, top: 0, width: 1, height: 1 };

    // Emerald / teal / ice (hunter-green product surface)
    const emerald = "61, 255, 154";
    const teal = "47, 217, 176";
    const ice = "232, 255, 244";
    const LERP = 0.07;

    const updateRect = () => {
      const r = canvas.getBoundingClientRect();
      rect.left = r.left;
      rect.top = r.top;
      rect.width = r.width || 1;
      rect.height = r.height || 1;
    };

    const resize = () => {
      const parent = canvas.parentElement;
      if (!parent) return;
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const w = parent.clientWidth;
      const h = parent.clientHeight;
      canvas.width = Math.max(1, Math.floor(w * dpr));
      canvas.height = Math.max(1, Math.floor(h * dpr));
      canvas.style.width = `${w}px`;
      canvas.style.height = `${h}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      dims.w = w;
      dims.h = h;
      updateRect();
    };

    resize();
    const ro = new ResizeObserver(resize);
    if (canvas.parentElement) ro.observe(canvas.parentElement);

    const onMove = (e: MouseEvent) => {
      mouse.tx = (e.clientX - rect.left) / rect.width;
      mouse.ty = (e.clientY - rect.top) / rect.height;
    };
    const onLeave = () => {
      mouse.tx = 0.5;
      mouse.ty = 0.5;
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseout", onLeave);

    const drawStaticFrame = () => {
      const { w, h } = dims;
      ctx.clearRect(0, 0, w, h);
      paintField(ctx, w, h, 0, 0.5, emerald, teal, ice, true);
    };

    const draw = (time: number) => {
      if (disposed) return;
      const { w, h } = dims;
      mouse.x += (mouse.tx - mouse.x) * LERP;
      mouse.y += (mouse.ty - mouse.y) * LERP;
      ctx.clearRect(0, 0, w, h);
      paintField(ctx, w, h, time, mouse.x, emerald, teal, ice, false);
      raf = requestAnimationFrame(draw);
    };

    if (reduceMotion) {
      drawStaticFrame();
    } else {
      raf = requestAnimationFrame(draw);
    }

    return () => {
      disposed = true;
      cancelAnimationFrame(raf);
      ro.disconnect();
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseout", onLeave);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      className="cluster-field"
      aria-hidden="true"
      style={{
        position: "absolute",
        inset: 0,
        zIndex: 0,
        pointerEvents: "none",
        touchAction: "none",
      }}
    />
  );
}

function paintField(
  ctx: CanvasRenderingContext2D,
  w: number,
  h: number,
  time: number,
  mx: number,
  emerald: string,
  teal: string,
  ice: string,
  staticMode: boolean,
) {
  const LINE_COUNT = 48;
  const VERTICAL_LINES = 22;
  const WAVE_SPEED = staticMode ? 0 : 0.00038;
  const WAVE_AMP = 20;
  const horizonY = h * 0.62;
  const perspective = 0.72;

  // Soft vignette underlay so type stays readable.
  const fog = ctx.createRadialGradient(
    w * 0.5,
    h * 0.42,
    40,
    w * 0.5,
    h * 0.5,
    Math.max(w, h) * 0.72,
  );
  fog.addColorStop(0, "rgba(61, 255, 154, 0.045)");
  fog.addColorStop(0.45, "rgba(6, 17, 12, 0.15)");
  fog.addColorStop(1, "rgba(3, 8, 6, 0.55)");
  ctx.fillStyle = fog;
  ctx.fillRect(0, 0, w, h);

  for (let i = 0; i < LINE_COUNT; i++) {
    const t = i / (LINE_COUNT - 1);
    const depth = Math.pow(t, perspective);
    const y = horizonY - horizonY * (1 - depth) * 0.95;
    if (y < -30 || y > h + 30) continue;

    ctx.beginPath();
    const freq = 2.4 + depth * 4.2;
    const phase = time * WAVE_SPEED * (1 + depth * 2.1) + depth * 6.2;
    const amp = WAVE_AMP * (0.45 + depth * 1.85);
    const mouseOffset = (mx - 0.5) * 46 * depth;

    for (let x = 0; x <= w; x += 3) {
      const nx = x / w;
      const wave =
        Math.sin(nx * Math.PI * freq + phase) * amp +
        Math.sin(nx * Math.PI * freq * 1.7 + phase * 0.6) * amp * 0.34 +
        Math.cos(nx * Math.PI * 3.1 + phase * 1.25) * amp * 0.14;
      const py = y + wave + mouseOffset;
      if (x === 0) ctx.moveTo(x, py);
      else ctx.lineTo(x, py);
    }

    const closeness = 1 - t;
    const alpha = closeness * 0.52 + 0.035;
    const lineWidth = closeness * 1.55 + 0.35;
    let strokeColor: string;
    if (closeness > 0.62) strokeColor = `rgba(${emerald}, ${alpha})`;
    else if (closeness > 0.28) strokeColor = `rgba(${teal}, ${alpha * 0.9})`;
    else strokeColor = `rgba(${ice}, ${alpha * 0.48})`;

    if (closeness > 0.78) {
      ctx.strokeStyle = `rgba(${emerald}, ${(closeness - 0.78) * 0.2})`;
      ctx.lineWidth = lineWidth * 5;
      ctx.lineCap = "round";
      ctx.stroke();
    }

    ctx.strokeStyle = strokeColor;
    ctx.lineWidth = lineWidth;
    ctx.lineCap = "round";
    ctx.stroke();
  }

  for (let i = 0; i < VERTICAL_LINES; i++) {
    const nx = i / (VERTICAL_LINES - 1);
    const x = nx * w;
    ctx.beginPath();
    for (let yi = 0; yi <= horizonY; yi += 4) {
      const t = yi / horizonY;
      const depth = Math.pow(t, perspective);
      const spread = 0.5 + depth * 0.5;
      const px = x + (nx - 0.5) * w * (1 - spread) * 0.42;
      const phase = time * WAVE_SPEED * 0.85;
      const wobble =
        Math.sin(nx * 7 + phase + depth * 4) * 3.2 * depth +
        (mx - 0.5) * 22 * depth;
      const py = yi + wobble;
      if (yi === 0) ctx.moveTo(px + wobble, py);
      else ctx.lineTo(px + wobble, py);
    }
    const alpha = 0.02 + Math.sin(nx * Math.PI) * 0.03;
    ctx.strokeStyle = `rgba(${ice}, ${alpha})`;
    ctx.lineWidth = 0.6;
    ctx.stroke();
  }

  // Job sparks traveling the field — "video" motion signature.
  const pulseCount = 7;
  for (let p = 0; p < pulseCount; p++) {
    const lineIndex = Math.floor(6 + p * 6);
    if (lineIndex >= LINE_COUNT) continue;
    const t = lineIndex / (LINE_COUNT - 1);
    const depth = Math.pow(t, perspective);
    const y = horizonY - horizonY * (1 - depth) * 0.95;
    const freq = 2.4 + depth * 4.2;
    const phase = time * WAVE_SPEED * (1 + depth * 2.1) + depth * 6.2;
    const amp = WAVE_AMP * (0.45 + depth * 1.85);
    const mouseOffset = (mx - 0.5) * 46 * depth;
    const pulsePhase = staticMode
      ? (p / pulseCount) * 0.7 + 0.15
      : (time * 0.00014 + p / pulseCount) % 1;
    const px = pulsePhase * w;
    const pnx = pulsePhase;
    const wave =
      Math.sin(pnx * Math.PI * freq + phase) * amp +
      Math.sin(pnx * Math.PI * freq * 1.7 + phase * 0.6) * amp * 0.34 +
      Math.cos(pnx * Math.PI * 3.1 + phase * 1.25) * amp * 0.14;
    const py = y + wave + mouseOffset;
    const closeness = 1 - t;
    const radius = closeness * 3.2 + 1.4;

    ctx.beginPath();
    ctx.arc(px, py, radius * 7, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(${emerald}, ${0.05 * closeness})`;
    ctx.fill();

    ctx.beginPath();
    ctx.arc(px, py, radius * 2.6, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(${emerald}, ${0.16 * closeness})`;
    ctx.fill();

    ctx.beginPath();
    ctx.arc(px, py, radius, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(${emerald}, ${0.55 + closeness * 0.4})`;
    ctx.fill();
  }
}
