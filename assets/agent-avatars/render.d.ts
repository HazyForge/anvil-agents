export type EyeShape = "ellipse" | "round-rect" | "crescent" | "ring";

export interface FaceDisk {
  cx: number;
  cy: number;
  r: number;
  fill: string;
  stroke: string;
  strokeWidth: number;
}

export interface EyeSpec {
  id: string;
  shape: EyeShape;
  x: number;
  y: number;
  rx: number;
  ry: number;
  fill: string;
  rotate?: number;
  opacity?: number;
  radius?: number;
  ring?: number;
  glow?: number;
  pupil?: boolean;
}

export interface FaceSpec {
  id: string;
  label: string;
  legacy?: string[];
  eyes: EyeSpec[];
}

export interface ScenePack {
  version: number;
  product: string;
  viewBox: string;
  face: FaceDisk;
  faces: FaceSpec[];
}

export function resolveFaceId(pack: ScenePack, raw: string | null | undefined): string;
export function findFace(pack: ScenePack, id: string | null | undefined): FaceSpec | undefined;
export function renderFace(face: FaceSpec, pack: ScenePack): string;
export function renderAllFaces(pack: ScenePack): Record<string, string>;
