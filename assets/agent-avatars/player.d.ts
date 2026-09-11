export interface AnimationKeyframe {
  t: number;
  v: number;
}

export interface AnimationClip {
  targets: string;
  property: "scaleY" | "translateX" | string;
  durationMs: number;
  keyframes: AnimationKeyframe[];
}

export interface AnimationSpec {
  version: number;
  product?: string;
  honorReducedMotion?: boolean;
  clips: Record<string, AnimationClip>;
  idle?: {
    blinkEveryMs?: number[];
    glanceEveryMs?: number[];
    doubleBlinkChance?: number;
    doubleBlinkGapMs?: number;
  };
}

export function attachIdle(
  svg: SVGElement,
  spec: AnimationSpec,
  options?: { seed?: string; reducedMotion?: boolean },
): () => void;

export function prefersReducedMotion(): boolean;
