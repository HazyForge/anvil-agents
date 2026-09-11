function escapeXml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function round(n) {
  const v = Number(n);
  if (!Number.isFinite(v)) {
    return 0;
  }
  return Math.round(v * 1000) / 1000;
}

function fmt(n) {
  const v = round(n);
  return Number.isInteger(v) ? String(v) : String(v);
}

/**
 * Resolve a stored icon value to a face id.
 * Accepts face id, anvil-avatar:id, /avatars/{id}.svg, and legacy robot-NN.jpg paths.
 * @param {{ faces: { id: string, legacy?: string[] }[] }} pack
 * @param {string | null | undefined} raw
 * @returns {string}
 */
export function resolveFaceId(pack, raw) {
  const value = String(raw ?? "").trim();
  if (!value || !pack?.faces) {
    return "";
  }
  const faces = pack.faces;
  const has = (id) => faces.some((face) => face.id === id);
  if (has(value)) {
    return value;
  }
  if (value.startsWith("anvil-avatar:")) {
    const id = value.slice("anvil-avatar:".length).trim();
    return has(id) ? id : "";
  }
  const svgName = value.match(/(?:^|\/)([a-z0-9-]+)\.svg$/i);
  if (svgName && has(svgName[1])) {
    return svgName[1];
  }
  const lower = value.toLowerCase();
  for (const face of faces) {
    for (const legacy of face.legacy ?? []) {
      if (legacy === value || legacy.toLowerCase() === lower) {
        return face.id;
      }
    }
  }
  const robot = value.match(/(?:^|\/)(robot-\d{2})(?:\.jpe?g)?$/i);
  if (robot) {
    for (const face of faces) {
      if ((face.legacy ?? []).includes(robot[1])) {
        return face.id;
      }
    }
  }
  return "";
}

/** @param {{ faces: { id: string }[] }} pack */
export function findFace(pack, id) {
  const resolved = resolveFaceId(pack, id);
  if (!resolved) {
    return undefined;
  }
  return pack.faces.find((face) => face.id === resolved);
}

function crescentPath(rx, ry) {
  const rxs = fmt(rx);
  const rys = fmt(ry);
  const inner = fmt(ry * 0.42);
  const drop = fmt(ry * 0.18);
  return `M ${fmt(-rx)},${drop} A ${rxs} ${rys} 0 0 1 ${rxs},${drop} A ${rxs} ${inner} 0 0 0 ${fmt(-rx)},${drop} Z`;
}

function eyeBody(eye) {
  const opacity =
    eye.opacity != null ? ` opacity="${fmt(eye.opacity)}"` : "";
  if (eye.shape === "crescent") {
    return `<path class="eye-body" d="${crescentPath(eye.rx, eye.ry)}" fill="${escapeXml(eye.fill)}"${opacity}/>`;
  }
  if (eye.shape === "round-rect") {
    const w = eye.rx * 2;
    const h = eye.ry * 2;
    const radius = eye.radius ?? Math.min(eye.rx, eye.ry) * 0.35;
    return `<rect class="eye-body" x="${fmt(-eye.rx)}" y="${fmt(-eye.ry)}" width="${fmt(w)}" height="${fmt(h)}" rx="${fmt(radius)}" fill="${escapeXml(eye.fill)}"${opacity}/>`;
  }
  if (eye.shape === "ring") {
    const ring = eye.ring ?? 2;
    return [
      `<ellipse class="eye-body" cx="0" cy="0" rx="${fmt(eye.rx)}" ry="${fmt(eye.ry)}" fill="none" stroke="${escapeXml(eye.fill)}" stroke-width="${fmt(ring)}"${opacity}/>`,
      `<ellipse class="eye-iris" cx="0" cy="0" rx="${fmt(Math.max(1.2, eye.rx - ring * 1.15))}" ry="${fmt(Math.max(1.2, eye.ry - ring * 1.15))}" fill="${escapeXml(eye.fill)}"${opacity}/>`,
    ].join("");
  }
  return `<ellipse class="eye-body" cx="0" cy="0" rx="${fmt(eye.rx)}" ry="${fmt(eye.ry)}" fill="${escapeXml(eye.fill)}"${opacity}/>`;
}

function eyeGlow(eye) {
  const glow = eye.glow ?? 0.22;
  if (glow <= 0) {
    return "";
  }
  const opacity = (eye.opacity ?? 1) * glow;
  return `<ellipse class="eye-glow" cx="0" cy="0" rx="${fmt(eye.rx * 1.55)}" ry="${fmt(eye.ry * 1.45)}" fill="${escapeXml(eye.fill)}" opacity="${fmt(opacity)}"/>`;
}

function eyeShine(eye) {
  if (eye.shape === "crescent") {
    return "";
  }
  const sx = -eye.rx * 0.32;
  const sy = -eye.ry * 0.38;
  const r = Math.max(0.8, Math.min(eye.rx, eye.ry) * 0.22);
  return `<circle class="eye-shine" cx="${fmt(sx)}" cy="${fmt(sy)}" r="${fmt(r)}" fill="#ffffff" opacity="0.38"/>`;
}

function eyePupil(eye) {
  if (!eye.pupil) {
    return "";
  }
  const suffix = eye.id.replace(/^eye-/, "");
  const prx = Math.max(0.7, eye.rx * 0.28);
  const pry = Math.max(0.9, eye.ry * 0.34);
  return `<ellipse id="pupil-${escapeXml(suffix)}" data-part="pupil" data-base-transform="" cx="0" cy="${fmt(eye.ry * 0.06)}" rx="${fmt(prx)}" ry="${fmt(pry)}" fill="#070b09" opacity="0.42"/>`;
}

function renderEye(eye, pack) {
  const cx = pack.face.cx + eye.x;
  const cy = pack.face.cy + eye.y;
  const rotate = eye.rotate ? ` rotate(${fmt(eye.rotate)})` : "";
  const base = `translate(${fmt(cx)} ${fmt(cy)})${rotate}`;
  return [
    `<g id="${escapeXml(eye.id)}" data-part="eye" data-base-transform="${base}" transform="${base}">`,
    eyeGlow(eye),
    eyeBody(eye),
    eyePupil(eye),
    eyeShine(eye),
    `</g>`,
  ].join("");
}

/**
 * Render one face to an SVG string. No CSS. Named groups are the scene graph.
 * @param {{ id: string, label: string, eyes: object[] }} face
 * @param {{ viewBox: string, product?: string, face: { cx: number, cy: number, r: number, fill: string, stroke: string, strokeWidth: number } }} pack
 */
export function renderFace(face, pack) {
  const title = escapeXml(face.label);
  const faceSpec = pack.face;
  const innerR = faceSpec.r - 2.1;
  const eyes = (face.eyes ?? []).map((eye) => renderEye(eye, pack)).join("");
  return [
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${escapeXml(pack.viewBox)}" data-anvil-avatar="${escapeXml(face.id)}" data-anvil-product="Anvil Agents" role="img" aria-label="${title}">`,
    `<title>${title}</title>`,
    `<g id="face">`,
    `<circle id="face-disk" cx="${fmt(faceSpec.cx)}" cy="${fmt(faceSpec.cy)}" r="${fmt(faceSpec.r)}" fill="${escapeXml(faceSpec.fill)}" stroke="${escapeXml(faceSpec.stroke)}" stroke-width="${fmt(faceSpec.strokeWidth)}"/>`,
    `<circle id="face-inner" cx="${fmt(faceSpec.cx)}" cy="${fmt(faceSpec.cy)}" r="${fmt(innerR)}" fill="none" stroke="${escapeXml(faceSpec.stroke)}" stroke-width="0.75" opacity="0.35"/>`,
    `</g>`,
    `<g id="eyes">`,
    eyes,
    `</g>`,
    `</svg>`,
  ].join("");
}

export function renderAllFaces(pack) {
  return Object.fromEntries(pack.faces.map((face) => [face.id, renderFace(face, pack)]));
}
