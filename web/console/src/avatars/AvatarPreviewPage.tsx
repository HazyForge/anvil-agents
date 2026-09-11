import { useState } from "react";
import { IconPicker } from "../components/IconPicker";
import { AVATAR_FACES } from "./pack";
import { AgentFace } from "./AgentFace";

/** Local-only gallery so faces can be checked without OIDC. Stripped from production builds. */
export function AvatarPreviewPage() {
  const [icon, setIcon] = useState("forge");
  const [screenshot, setScreenshot] = useState("");

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-title">Anvil Agents faces</div>
          <div className="brand-sub">Local preview · not shipped in production</div>
        </div>
      </header>
      <main className="main" style={{ padding: "1rem 1.25rem" }}>
        <div className="banner banner-info">
          Shared pack in <span className="mono">assets/agent-avatars</span>. Stored annotation is
          the face id (portable to desktop and later native clients).
        </div>
        <div className="avatar-preview-grid">
          {AVATAR_FACES.map((face) => (
            <button
              key={face.id}
              type="button"
              className={["avatar-preview-card", icon === face.id ? "avatar-pick-btn-selected" : ""]
                .filter(Boolean)
                .join(" ")}
              onClick={() => setIcon(face.id)}
            >
              <AgentFace faceId={face.id} className="avatar-preview-face" />
              <span className="avatar-pick-label">{face.label}</span>
              <span className="mono text-mute" style={{ fontSize: "0.65rem" }}>
                {face.id}
              </span>
            </button>
          ))}
        </div>
        <div className="panel" style={{ marginTop: "1rem" }}>
          <div className="panel-body">
            <IconPicker
              label="Profile avatar / icon"
              help="Same picker as composition editors. Saves the face id."
              icon={icon}
              screenshot={screenshot}
              onIconChange={setIcon}
              onScreenshotChange={setScreenshot}
            />
          </div>
        </div>
      </main>
    </div>
  );
}
