export interface ToolEntryForm {
  name: string;
  description: string;
  setupScript: string;
  verifyCommand: string;
}

export interface ToolSetForm {
  name: string;
  description: string;
  tools: ToolEntryForm[];
}

export function emptyToolEntry(): ToolEntryForm {
  return { name: "", description: "", setupScript: "", verifyCommand: "" };
}

export function emptyToolSetForm(): ToolSetForm {
  return {
    name: "",
    description: "",
    tools: [emptyToolEntry()],
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

export function formFromToolSetSpec(spec: Record<string, unknown>, name = ""): ToolSetForm {
  const toolsRaw = Array.isArray(spec.tools) ? spec.tools : [];
  const tools = toolsRaw.map((raw) => {
    const item = asRecord(raw);
    const verify = Array.isArray(item.verifyCommand)
      ? item.verifyCommand.map(String).join(" ")
      : String(item.verifyCommand ?? "");
    return {
      name: String(item.name ?? ""),
      description: String(item.description ?? ""),
      setupScript: String(item.setupScript ?? ""),
      verifyCommand: verify,
    };
  });
  return {
    name,
    description: String(spec.description ?? ""),
    tools: tools.length ? tools : [emptyToolEntry()],
  };
}

export function buildToolSetSpec(form: ToolSetForm): Record<string, unknown> {
  const tools = form.tools
    .filter((t) => t.name.trim())
    .map((t) => {
      const entry: Record<string, unknown> = { name: t.name.trim() };
      if (t.description.trim()) {
        entry.description = t.description.trim();
      }
      if (t.setupScript.trim()) {
        entry.setupScript = t.setupScript;
      }
      const parts = t.verifyCommand
        .trim()
        .split(/\s+/)
        .filter(Boolean);
      if (parts.length) {
        entry.verifyCommand = parts;
      }
      return entry;
    });
  const spec: Record<string, unknown> = {};
  if (form.description.trim()) {
    spec.description = form.description.trim();
  }
  if (tools.length) {
    spec.tools = tools;
  }
  return spec;
}

export function validateToolSetForm(form: ToolSetForm, isCreate: boolean): string | null {
  if (isCreate && !form.name.trim()) {
    return "Name is required";
  }
  if (isCreate && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(form.name.trim())) {
    return "Name must be a DNS-1123 label";
  }
  return null;
}
