/**
 * Anvil Agents avatar idle player.
 *
 * Porting hook (Swift / Android / later desktop):
 * 1. Load scene.json + animation.json, or the generated SVG.
 * 2. Find groups with data-part="eye" (ids eye-left, eye-right, optional eye-center).
 * 3. Blink: compose scale(1, sy) AFTER the group's data-base-transform.
 * 4. Glance: compose translate(x, 0) on data-part="pupil" groups.
 * 5. Do not use CSS keyframes as the source of truth. This file tweens SVG
 *    transform attributes so the same math can be copied into Core Animation
 *    or ObjectAnimator.
 *
 * @param {SVGElement} svg
 * @param {object} spec animation.json
 * @param {{ seed?: string, reducedMotion?: boolean }} [options]
 * @returns {() => void} dispose
 */
export function attachIdle(svg, spec, options = {}) {
  if (!svg || !spec?.clips) {
    return () => {};
  }
  const reduced =
    options.reducedMotion ??
    (spec.honorReducedMotion !== false && prefersReducedMotion());
  if (reduced) {
    return () => {};
  }

  const eyes = [...svg.querySelectorAll("[data-part='eye']")];
  const pupils = [...svg.querySelectorAll("[data-part='pupil']")];
  if (eyes.length === 0) {
    return () => {};
  }

  const rand = mulberry32(hashString(options.seed ?? svg.getAttribute("data-anvil-avatar") ?? "face"));
  const pending = new Set();
  let blinkTimer = 0;
  let glanceTimer = 0;
  let stopped = false;

  function playClip(clipName, elements, apply, onDone) {
    const clip = spec.clips[clipName];
    if (!clip || elements.length === 0) {
      onDone?.();
      return () => {};
    }
    const duration = Math.max(16, Number(clip.durationMs) || 140);
    const keyframes = Array.isArray(clip.keyframes) ? clip.keyframes : [];
    const t0 = performance.now();
    let raf = 0;
    const handle = { cancel() {} };
    function frame(now) {
      if (stopped) {
        return;
      }
      const p = Math.min(1, (now - t0) / duration);
      const value = sample(keyframes, p);
      for (const el of elements) {
        apply(el, value);
      }
      if (p < 1) {
        raf = requestAnimationFrame(frame);
      } else {
        pending.delete(handle);
        onDone?.();
      }
    }
    raf = requestAnimationFrame(frame);
    handle.cancel = () => {
      cancelAnimationFrame(raf);
      pending.delete(handle);
    };
    pending.add(handle);
    return handle.cancel;
  }

  function resetEyes() {
    for (const el of eyes) {
      setTransform(el, 1, "scaleY");
    }
  }

  function resetPupils() {
    for (const el of pupils) {
      setTransform(el, 0, "translateX");
    }
  }

  function scheduleBlink() {
    if (stopped) {
      return;
    }
    const [min, max] = range(spec.idle?.blinkEveryMs, 2800, 6000);
    blinkTimer = window.setTimeout(() => {
      playClip("blink", eyes, (el, v) => setTransform(el, v, "scaleY"), () => {
        resetEyes();
        if (!stopped && rand() < Number(spec.idle?.doubleBlinkChance ?? 0)) {
          blinkTimer = window.setTimeout(() => {
            playClip("blink", eyes, (el, v) => setTransform(el, v, "scaleY"), () => {
              resetEyes();
              scheduleBlink();
            });
          }, Number(spec.idle?.doubleBlinkGapMs ?? 120));
          return;
        }
        scheduleBlink();
      });
    }, min + rand() * (max - min));
  }

  function scheduleGlance() {
    if (stopped || pupils.length === 0) {
      return;
    }
    const [min, max] = range(spec.idle?.glanceEveryMs, 9000, 16000);
    glanceTimer = window.setTimeout(() => {
      playClip("glance", pupils, (el, v) => setTransform(el, v, "translateX"), () => {
        resetPupils();
        scheduleGlance();
      });
    }, min + rand() * (max - min));
  }

  scheduleBlink();
  scheduleGlance();

  return () => {
    stopped = true;
    window.clearTimeout(blinkTimer);
    window.clearTimeout(glanceTimer);
    for (const handle of pending) {
      handle.cancel();
    }
    pending.clear();
    resetEyes();
    resetPupils();
  };
}

export function prefersReducedMotion() {
  try {
    return Boolean(window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches);
  } catch {
    return false;
  }
}

function setTransform(el, value, property) {
  const base = el.getAttribute("data-base-transform") || "";
  if (property === "scaleY") {
    const sy = Number(value);
    el.setAttribute("transform", sy === 1 || !Number.isFinite(sy) ? base : `${base} scale(1 ${round(sy)})`);
    return;
  }
  if (property === "translateX") {
    const x = Number(value);
    el.setAttribute("transform", x === 0 || !Number.isFinite(x) ? base : `${base} translate(${round(x)} 0)`);
  }
}

function sample(keyframes, p) {
  if (keyframes.length === 0) {
    return 1;
  }
  if (p <= keyframes[0].t) {
    return Number(keyframes[0].v);
  }
  const last = keyframes[keyframes.length - 1];
  if (p >= last.t) {
    return Number(last.v);
  }
  for (let i = 1; i < keyframes.length; i += 1) {
    const b = keyframes[i];
    if (p <= b.t) {
      const a = keyframes[i - 1];
      const span = b.t - a.t || 1;
      const u = (p - a.t) / span;
      return Number(a.v) + (Number(b.v) - Number(a.v)) * u;
    }
  }
  return Number(last.v);
}

function range(pair, min, max) {
  if (Array.isArray(pair) && pair.length >= 2) {
    return [Number(pair[0]), Number(pair[1])];
  }
  return [min, max];
}

function round(n) {
  return Math.round(Number(n) * 1000) / 1000;
}

function hashString(input) {
  let h = 2166136261;
  const s = String(input);
  for (let i = 0; i < s.length; i += 1) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a += 0x6d2b79f5;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
