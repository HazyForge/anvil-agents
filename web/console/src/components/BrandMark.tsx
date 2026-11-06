import { useId } from "react";

/** Canonical Hazy Forge mark (blue → cyan → ice). Decorative next to the wordmark. */
export function BrandMark({ className = "brand-mark" }: { className?: string }) {
  const gradientId = `hf-mark-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  return (
    <svg className={className} viewBox="0 0 160 99.2" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id={gradientId} x1="34.01" x2="157.7" y1="77.74" y2="12.58" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#3786F7" />
          <stop offset=".4896" stopColor="#50D8FA" />
          <stop offset="1" stopColor="#E9FDFF" />
        </linearGradient>
      </defs>
      <path
        fill={`url(#${gradientId})`}
        d="m23.5 57.1-9.6 15.8h9.9c2.5 0 6.1-1.6 8.2-5l7.3-10.8h-15.8zm32.2-53.7-21.6 34.1h16.6l21.2-34.7h-16.1l-0.1 0.6zm41.9 9.2c-4.2 0-10.3 2.8-12.7 5.9l-11.2 18.8c-1.2 2.1-4.7 2.8-7.1 2.8h-56l-7.8 14.2 60.6 0.1-22.1 34.4h11.7c4.4 0 7.3-1.7 9.6-5l31.1-52.5c0.9-1.4 4.8-3.9 8-3.9h47.4l8.5-14.8h-60zm-9.9 47.6-21.9 36h12.7l21.9-36h-12.7zm11-17.2-9.1 14.4h36.7c2.2 0 5.9-1.3 7.4-3.6l6.8-10.8h-41.8z"
      />
    </svg>
  );
}

/**
 * Mark + wordmark. The product title keeps its DOM text; the last word drops to
 * the Barlow caps line ("Anvil Agents" / "Console").
 */
export function BrandLockup({ title }: { title: string }) {
  const words = title.trim().split(/\s+/).filter(Boolean);
  const head = words.length > 1 ? words.slice(0, -1).join(" ") : title;
  const tail = words.length > 1 ? words[words.length - 1] : "";
  return (
    <div className="brand-lockup">
      <BrandMark />
      <div className="wordmark">
        <strong className="brand-title">{head}</strong>
        {tail ? <small className="brand-sub-word"> {tail}</small> : null}
      </div>
    </div>
  );
}
