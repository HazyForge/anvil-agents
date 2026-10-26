import { Link, useLocation } from "react-router-dom";
import { Icon } from "./Icon";
import { leafForPath } from "./NamespaceTree";

interface Props {
  compositionRead: boolean;
  controlsRead: boolean;
  chatEnabled: boolean;
}

type DockId = "runs" | "chat" | "profiles" | "library" | "controls";

function dockForLeaf(leaf: string): DockId {
  switch (leaf) {
    case "agent-runs":
      return "runs";
    case "chat":
      return "chat";
    case "controls":
      return "controls";
    case "profiles":
      return "profiles";
    default:
      return "library";
  }
}

/**
 * Icon dock (narrow desktop) and bottom tabs (phone). Same destinations the
 * namespace tree opens for the active namespace; hidden on wide screens where
 * the tree is the navigation.
 */
export function DockNav({ compositionRead, controlsRead, chatEnabled }: Props) {
  const location = useLocation();
  const active = dockForLeaf(leafForPath(location.pathname));
  const items: Array<{ id: DockId; to: string; label: string; icon: string }> = [
    { id: "runs", to: "/", label: "Runs", icon: "runs" },
  ];
  if (chatEnabled) {
    items.push({ id: "chat", to: "/chat", label: "Chat", icon: "chat" });
  }
  if (compositionRead) {
    items.push({ id: "profiles", to: "/profiles", label: "Profiles", icon: "profiles" });
    items.push({ id: "library", to: "/library", label: "Library", icon: "library" });
  }
  if (controlsRead) {
    items.push({ id: "controls", to: "/controls", label: "Controls", icon: "controls" });
  }

  return (
    <nav className="dock" aria-label="Console sections">
      {items.map((item) => (
        <Link
          key={item.id}
          to={item.to}
          className={`dock-item${active === item.id ? " active" : ""}`}
          aria-current={active === item.id ? "page" : undefined}
        >
          <Icon name={item.icon} />
          <span>{item.label}</span>
        </Link>
      ))}
    </nav>
  );
}
