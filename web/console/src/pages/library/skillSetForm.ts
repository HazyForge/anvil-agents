export interface SkillEntryForm {
  name: string;
  description: string;
  content: string;
}

export interface SkillSetForm {
  name: string;
  description: string;
  /** Namespace-global: auto-attach to every AgentRun unless excludeGlobal. */
  global: boolean;
  skills: SkillEntryForm[];
}

export function emptySkillEntry(): SkillEntryForm {
  return { name: "", description: "", content: "" };
}

export function emptySkillSetForm(): SkillSetForm {
  return {
    name: "",
    description: "",
    global: false,
    skills: [emptySkillEntry()],
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

export function formFromSkillSetSpec(spec: Record<string, unknown>, name = ""): SkillSetForm {
  const skillsRaw = Array.isArray(spec.skills) ? spec.skills : [];
  const skills = skillsRaw.map((raw) => {
    const item = asRecord(raw);
    return {
      name: String(item.name ?? ""),
      description: String(item.description ?? ""),
      content: String(item.content ?? ""),
    };
  });
  return {
    name,
    description: String(spec.description ?? ""),
    global: Boolean(spec.global),
    skills: skills.length ? skills : [emptySkillEntry()],
  };
}

export function buildSkillSetSpec(form: SkillSetForm): Record<string, unknown> {
  const skills = form.skills
    .filter((s) => s.name.trim())
    .map((s) => {
      const entry: Record<string, unknown> = { name: s.name.trim() };
      if (s.description.trim()) {
        entry.description = s.description.trim();
      }
      if (s.content.trim()) {
        entry.content = s.content;
      }
      return entry;
    });
  const spec: Record<string, unknown> = {};
  if (form.description.trim()) {
    spec.description = form.description.trim();
  }
  if (form.global) {
    spec.global = true;
  }
  if (skills.length) {
    spec.skills = skills;
  }
  return spec;
}

export function validateSkillSetForm(form: SkillSetForm, isCreate: boolean): string | null {
  if (isCreate && !form.name.trim()) {
    return "Name is required";
  }
  if (isCreate && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(form.name.trim())) {
    return "Name must be a DNS-1123 label";
  }
  return null;
}
