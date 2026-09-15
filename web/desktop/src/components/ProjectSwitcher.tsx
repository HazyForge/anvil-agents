import type { CompositionDocument } from '../api/client';
import { DesktopIcon } from './AgentAvatar';

export type ChatProject = {id: string; name: string};

// The navigation component knows projects; the remote adapter currently maps
// one project to one configured namespace. API authorization remains unchanged.
export function remoteChatProjects(namespaces: string[], selected: string): ChatProject[] {
  return [...new Set([...namespaces, selected].map(name => name.trim()).filter(Boolean))].map(id => ({
    id,
    name: id === 'anvilhub' ? 'Anvil' : id.split(/[-_]+/).map(word => word.charAt(0).toUpperCase() + word.slice(1)).join(' '),
  }));
}

export function ProjectSwitcher({projects, selected, disabled, onChange}: {
  projects: ChatProject[]; selected: string; disabled?: boolean; onChange: (id: string) => void;
}) {
  return <label className="chat-project-switcher">
    <span className="chat-project-label">Project</span>
    <span className="chat-project-control">
      <DesktopIcon kind="project"/>
      <select aria-label="Project" value={selected} disabled={disabled} onChange={event => onChange(event.target.value)}>
        {projects.map(project => <option value={project.id} key={project.id}>{project.name}</option>)}
      </select>
    </span>
  </label>;
}

// Presentation metadata only; execution permissions remain with the Hub policy.
// Exact legacy identities bridge existing installations until their source-owned
// profiles adopt the label. A specialized "manager" name is not a designation.
export function projectManagers(profiles: CompositionDocument[], project: string): CompositionDocument[] {
  const roleLabel = 'control.anvil.hazyforge.io/chat-role';
  const designated = profiles.filter(profile => profile.metadata.labels?.[roleLabel] === 'project-manager');
  if (designated.length) return designated;
  const legacy: Record<string, string> = {
    anvilhub: 'anvil-primaris-agent-manager',
    'hazy-trade': 'hazy-trade-agent-manager',
  };
  return profiles.filter(profile => profile.metadata.name === legacy[project] && !profile.metadata.labels?.[roleLabel]);
}
