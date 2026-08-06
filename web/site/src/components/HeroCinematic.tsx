import { useEffect, useState } from "react";

/**
 * Cinematic hero media for Anvil Agents.
 *
 * A Grok Imagine–generated film plays full-bleed behind the hero copy
 * (anvil forge + Kubernetes job grid, hunter-green on ink). The still is
 * always rendered for instant first paint and as the video poster; when the
 * user prefers reduced motion or reduced data, we keep the still and skip
 * video playback entirely — no per-frame canvas, no WebGL, no 3D.
 */
export default function HeroCinematic() {
  const [motionOk, setMotionOk] = useState(true);

  useEffect(() => {
    const motion = window.matchMedia("(prefers-reduced-motion: reduce)");
    const data = window.matchMedia("(prefers-reduced-data: reduce)");
    const update = () => setMotionOk(!motion.matches && !data.matches);
    update();
    motion.addEventListener("change", update);
    data.addEventListener("change", update);
    return () => {
      motion.removeEventListener("change", update);
      data.removeEventListener("change", update);
    };
  }, []);

  return (
    <div className="hero-media" aria-hidden="true">
      <img
        className="hero-poster"
        src="/hero/hero-poster.jpg"
        alt=""
        fetchPriority="high"
        decoding="async"
      />
      {motionOk ? (
        <video
          className="hero-video"
          autoPlay
          muted
          loop
          playsInline
          preload="auto"
          poster="/hero/hero-poster.jpg"
          tabIndex={-1}
        >
          <source src="/hero/hero.mp4" type="video/mp4" />
        </video>
      ) : null}
    </div>
  );
}
