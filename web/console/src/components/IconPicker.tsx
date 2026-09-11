import { useMemo, useState } from "react";
import { AgentFace } from "../avatars/AgentFace";
import {
  BUILTIN_AVATARS,
  resolveIcon,
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
  /** Optional pack override (defaults to Anvil Agents faces). */
  avatars?: BuiltInAvatar[];
}

export function IconPicker({
  label = "Avatar / icon",
  help = "Pick an Anvil Agents face, or paste any image URL. The face id is stored on the CR and is portable to desktop and later native clients.",
  icon,
  screenshot,
  disabled,
  onIconChange,
  onScreenshotChange,
  avatars = BUILTIN_AVATARS,
}: Props) {
  const resolved = useMemo(() => resolveIcon(icon), [icon]);
  const [showCustom, setShowCustom] = useState(() => resolved?.kind === "url");

  const preview = resolved;
  const shotPreview = useMemo(() => resolveIconSrc(screenshot), [screenshot]);

  function selectBuiltin(avatar: BuiltInAvatar) {
    if (disabled) {
      return;
    }
    onIconChange(avatar.id);
    setShowCustom(false);
  }

  function clearIcon() {
    if (disabled) {
      return;
    }
    onIconChange("");
  }

  function isSelected(avatar: BuiltInAvatar): boolean {
    if (resolved?.kind === "face") {
      return resolved.id === avatar.id;
    }
    return icon === avatar.src || icon === avatar.id;
  }

  return (
    <div className="field icon-picker">
      <span className="label">{label}</span>
      {help ? <p className="field-help">{help}</p> : null}

      <div className="icon-picker-preview-row">
        <div className="icon-preview-tile" aria-hidden={!preview}>
          {preview?.kind === "face" ? (
            <AgentFace faceId={preview.id} className="icon-preview-face" />
          ) : preview ? (
            <img src={preview.src} alt="" className="icon-preview-img" />
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

      <div className="avatar-pick-grid" role="listbox" aria-label="Anvil Agents faces">
        {avatars.map((avatar) => {
          const selected = isSelected(avatar);
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
              <AgentFace faceId={avatar.id} className="avatar-pick-face" />
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
            placeholder="https://… or forge or /avatars/forge.svg or data:image/…"
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
  const resolved = resolveIcon(icon);
  const cls = `composition-avatar composition-avatar-${size}`;
  if (resolved?.kind === "face") {
    return <AgentFace faceId={resolved.id} className={cls} title={name} />;
  }
  if (resolved) {
    return <img src={resolved.src} alt="" className={cls} />;
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
