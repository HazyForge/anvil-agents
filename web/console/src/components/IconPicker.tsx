import { useMemo, useState } from "react";
import {
  BUILTIN_AVATARS,
  resolveIconSrc,
  type BuiltInAvatar,
} from "../utils/icons";

interface Props {
  label?: string;
  help?: string;
  icon: string;
  screenshot: string;
  disabled?: boolean;
  onIconChange: (next: string) => void;
  onScreenshotChange: (next: string) => void;
  /** Optional pack override (defaults to robot avatars). */
  avatars?: BuiltInAvatar[];
}

export function IconPicker({
  label = "Avatar / icon",
  help = "Pick a built-in robot avatar, or paste any image URL (https / data / console path).",
  icon,
  screenshot,
  disabled,
  onIconChange,
  onScreenshotChange,
  avatars = BUILTIN_AVATARS,
}: Props) {
  const [showCustom, setShowCustom] = useState(() => {
    const resolved = resolveIconSrc(icon);
    if (!resolved) {
      return false;
    }
    return !avatars.some((a) => a.src === resolved || a.id === icon);
  });

  const preview = useMemo(() => resolveIconSrc(icon), [icon]);
  const shotPreview = useMemo(() => resolveIconSrc(screenshot), [screenshot]);

  function selectBuiltin(avatar: BuiltInAvatar) {
    if (disabled) {
      return;
    }
    onIconChange(avatar.src);
    setShowCustom(false);
  }

  function clearIcon() {
    if (disabled) {
      return;
    }
    onIconChange("");
  }

  return (
    <div className="field icon-picker">
      <span className="label">{label}</span>
      {help ? <p className="field-help">{help}</p> : null}

      <div className="icon-picker-preview-row">
        <div className="icon-preview-tile" aria-hidden={!preview}>
          {preview ? (
            <img src={preview} alt="" className="icon-preview-img" />
          ) : (
            <span className="icon-preview-placeholder">No icon</span>
          )}
        </div>
        <div className="icon-picker-preview-meta">
          <span className="mono text-mute" style={{ wordBreak: "break-all" }}>
            {icon || "(none)"}
          </span>
          <div className="chip-row">
            <button
              type="button"
              className="btn btn-ghost"
              disabled={disabled || !icon}
              onClick={clearIcon}
            >
              Clear
            </button>
            <button
              type="button"
              className="btn btn-ghost"
              disabled={disabled}
              onClick={() => setShowCustom((v) => !v)}
            >
              {showCustom ? "Hide custom URL" : "Custom URL"}
            </button>
          </div>
        </div>
      </div>

      <div className="avatar-pick-grid" role="listbox" aria-label="Built-in avatars">
        {avatars.map((avatar) => {
          const selected = icon === avatar.src || icon === avatar.id;
          return (
            <button
              key={avatar.id}
              type="button"
              role="option"
              aria-selected={selected}
              className={["avatar-pick-btn", selected ? "avatar-pick-btn-selected" : ""]
                .filter(Boolean)
                .join(" ")}
              disabled={disabled}
              title={avatar.label}
              onClick={() => selectBuiltin(avatar)}
            >
              <img src={avatar.src} alt={avatar.label} className="avatar-pick-img" />
              <span className="avatar-pick-label">{avatar.label}</span>
            </button>
          );
        })}
      </div>

      {showCustom ? (
        <label className="field" style={{ marginTop: "0.5rem" }}>
          <span className="label">Custom icon URL</span>
          <input
            className="input mono"
            value={icon}
            disabled={disabled}
            onChange={(event) => onIconChange(event.target.value)}
            placeholder="https://… or /avatars/robot-01.jpg or data:image/…"
            autoComplete="off"
          />
        </label>
      ) : null}

      <label className="field" style={{ marginTop: "0.65rem" }}>
        <span className="label">Screenshot / banner (optional)</span>
        <p className="field-help">
          Wider image shown on the card header. Any https, data, or console path URL.
        </p>
        <input
          className="input mono"
          value={screenshot}
          disabled={disabled}
          onChange={(event) => onScreenshotChange(event.target.value)}
          placeholder="https://… or leave empty"
          autoComplete="off"
        />
        {shotPreview ? (
          <div className="screenshot-preview">
            <img src={shotPreview} alt="" className="screenshot-preview-img" />
          </div>
        ) : null}
      </label>
    </div>
  );
}

/** Compact avatar image or monogram fallback for cards and lists. */
export function CompositionAvatar({
  icon,
  name,
  size = "md",
}: {
  icon?: string;
  name: string;
  size?: "sm" | "md" | "lg";
}) {
  const src = resolveIconSrc(icon);
  const cls = `composition-avatar composition-avatar-${size}`;
  if (src) {
    return <img src={src} alt="" className={cls} />;
  }
  const initial = name
    .trim()
    .split(/[-_\s.]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase() ?? "")
    .join("") || "?";
  return (
    <span className={`${cls} composition-avatar-fallback`} aria-hidden>
      {initial}
    </span>
  );
}
