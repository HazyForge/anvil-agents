export type DataVolumeBackend =
  | ""
  | "codex"
  | "openCode"
  | "hermesAgent"
  | "openClaw"
  | "grokBuild"
  | "piAgent"
  | "custom";

export interface DataVolumeForm {
  name: string;
  description: string;
  agentName: string;
  backend: DataVolumeBackend;
  applicationName: string;
  profileName: string;
  profileVolumeName: string;
  claimName: string;
  mountPath: string;
  size: string;
  storageClassName: string;
  accessMode: string;
  notes: string;
  homeEnvName: string;
  homeEnvValue: string;
}

export function emptyDataVolumeForm(): DataVolumeForm {
  return {
    name: "",
    description: "",
    agentName: "",
    backend: "grokBuild",
    applicationName: "",
    profileName: "",
    profileVolumeName: "",
    claimName: "",
    mountPath: "/opt/anvil/grok-build",
    size: "10Gi",
    storageClassName: "",
    accessMode: "ReadWriteOnce",
    notes: "",
    homeEnvName: "GROK_BUILD_HOME",
    homeEnvValue: "/opt/anvil/grok-build",
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

export function formFromDataVolumeSpec(spec: Record<string, unknown>, name = ""): DataVolumeForm {
  const form = emptyDataVolumeForm();
  form.name = name;
  form.agentName = String(spec.agentName ?? "");
  form.backend = (String(spec.backend ?? "") as DataVolumeBackend) || "";
  form.applicationName = String(asRecord(spec.applicationRef).name ?? "");
  form.profileName = String(asRecord(spec.profileRef).name ?? "");
  form.profileVolumeName = String(spec.profileVolumeName ?? "");
  form.claimName = String(spec.claimName ?? "");
  form.mountPath = String(spec.mountPath ?? "");
  form.size = String(spec.size ?? "");
  form.storageClassName = String(spec.storageClassName ?? "");
  const modes = Array.isArray(spec.accessModes) ? spec.accessModes : [];
  form.accessMode = modes.length ? String(modes[0]) : "ReadWriteOnce";
  form.notes = String(spec.notes ?? "");
  form.description = form.notes;
  const extra = Array.isArray(spec.extraEnv) ? spec.extraEnv : [];
  if (extra.length > 0 && typeof extra[0] === "object" && extra[0]) {
    form.homeEnvName = String((extra[0] as { name?: string }).name ?? "");
    form.homeEnvValue = String((extra[0] as { value?: string }).value ?? "");
  }
  return form;
}

export function buildDataVolumeSpec(form: DataVolumeForm): Record<string, unknown> {
  const spec: Record<string, unknown> = {};
  if (form.agentName.trim()) {
    spec.agentName = form.agentName.trim();
  }
  if (form.backend) {
    spec.backend = form.backend;
  }
  if (form.applicationName.trim()) {
    spec.applicationRef = { name: form.applicationName.trim() };
  }
  if (form.profileName.trim()) {
    spec.profileRef = { name: form.profileName.trim() };
  }
  if (form.profileVolumeName.trim()) {
    spec.profileVolumeName = form.profileVolumeName.trim();
  }
  if (form.claimName.trim()) {
    spec.claimName = form.claimName.trim();
  }
  if (form.mountPath.trim()) {
    spec.mountPath = form.mountPath.trim();
  }
  if (form.size.trim()) {
    spec.size = form.size.trim();
  }
  if (form.storageClassName.trim()) {
    spec.storageClassName = form.storageClassName.trim();
  }
  if (form.accessMode.trim()) {
    spec.accessModes = [form.accessMode.trim()];
  }
  const notes = form.notes.trim() || form.description.trim();
  if (notes) {
    spec.notes = notes;
  }
  if (form.homeEnvName.trim() && form.homeEnvValue.trim()) {
    spec.extraEnv = [
      {
        name: form.homeEnvName.trim(),
        value: form.homeEnvValue.trim(),
      },
    ];
  }
  return spec;
}

export function validateDataVolumeForm(form: DataVolumeForm, isCreate: boolean): string | null {
  if (isCreate && !form.name.trim()) {
    return "Name is required";
  }
  if (isCreate && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(form.name.trim())) {
    return "Name must be a DNS-1123 label";
  }
  if (form.homeEnvName && !form.homeEnvValue.startsWith("/")) {
    return "Home env value must be an absolute path";
  }
  return null;
}

export const DATA_VOLUME_BACKENDS: {
  value: DataVolumeBackend;
  label: string;
  summary: string;
  defaultMount: string;
  homeEnvName: string;
}[] = [
  {
    value: "codex",
    label: "Codex",
    summary: "OpenAI Codex durable home",
    defaultMount: "/opt/anvil/codex",
    homeEnvName: "CODEX_HOME",
  },
  {
    value: "openCode",
    label: "OpenCode",
    summary: "OpenCode session / config home",
    defaultMount: "/opt/anvil/opencode",
    homeEnvName: "OPENCODE_HOME",
  },
  {
    value: "grokBuild",
    label: "Grok Build",
    summary: "xAI Grok Build durable home",
    defaultMount: "/opt/anvil/grok-build",
    homeEnvName: "GROK_BUILD_HOME",
  },
  {
    value: "hermesAgent",
    label: "Hermes Agent",
    summary: "Hermes agent home",
    defaultMount: "/opt/anvil/hermes",
    homeEnvName: "HERMES_HOME",
  },
  {
    value: "openClaw",
    label: "OpenClaw",
    summary: "OpenClaw agent home",
    defaultMount: "/opt/anvil/openclaw",
    homeEnvName: "OPENCLAW_HOME",
  },
  {
    value: "piAgent",
    label: "Pi Agent",
    summary: "Pi agent home",
    defaultMount: "/opt/anvil/pi",
    homeEnvName: "PI_HOME",
  },
  {
    value: "custom",
    label: "Custom",
    summary: "Custom durable path",
    defaultMount: "/opt/anvil/agent-home",
    homeEnvName: "AGENT_HOME",
  },
];

/** Apply backend defaults for mount path and home env when the user picks a backend card. */
export function applyBackendDefaults(form: DataVolumeForm, backend: DataVolumeBackend): DataVolumeForm {
  if (!backend) {
    return { ...form, backend };
  }
  const opt = DATA_VOLUME_BACKENDS.find((item) => item.value === backend);
  if (!opt) {
    return { ...form, backend };
  }
  return {
    ...form,
    backend,
    mountPath: opt.defaultMount,
    homeEnvName: opt.homeEnvName,
    homeEnvValue: opt.defaultMount,
  };
}
