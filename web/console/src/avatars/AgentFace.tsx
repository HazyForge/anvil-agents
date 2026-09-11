import { useEffect, useMemo, useRef } from "react";
import { attachIdle, avatarAnimation, avatarById, renderFace, avatarScene } from "./pack";

interface Props {
  faceId: string;
  className?: string;
  /** Play idle blink/glance. Honors prefers-reduced-motion inside the player. */
  animate?: boolean;
  decorative?: boolean;
  label?: string;
  title?: string;
}

/** Inline Anvil Agents face. SVG is inlined so the shared player can blink. */
export function AgentFace({
  faceId,
  className,
  animate = true,
  decorative = true,
  label,
  title,
}: Props) {
  const hostRef = useRef<HTMLSpanElement>(null);
  const face = avatarById(faceId);
  const markup = useMemo(() => (face ? renderFace(face, avatarScene) : ""), [face]);

  useEffect(() => {
    const svg = hostRef.current?.querySelector("svg");
    if (!svg || !animate || !face) {
      return;
    }
    return attachIdle(svg, avatarAnimation, { seed: face.id });
  }, [animate, face, markup]);

  if (!face || !markup) {
    return null;
  }

  const text = label || face.label;
  return (
    <span
      ref={hostRef}
      className={["agent-face", className].filter(Boolean).join(" ")}
      title={title || text}
      aria-hidden={decorative ? true : undefined}
      role={decorative ? undefined : "img"}
      aria-label={decorative ? undefined : text}
      dangerouslySetInnerHTML={{ __html: markup }}
    />
  );
}
