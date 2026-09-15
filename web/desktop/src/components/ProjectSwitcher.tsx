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
