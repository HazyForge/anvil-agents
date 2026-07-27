export interface VolumeEntryForm {
  name: string;
  purpose: string;
  mountPath: string;
  sizeRequest: string;
  accessMode: string;
}

export interface VolumeProfileForm {
  name: string;
  description: string;
  volumes: VolumeEntryForm[];
}

export function emptyVolumeEntry(): VolumeEntryForm {
  return {
    name: "home",
    purpose: "agent-home",
    mountPath: "/agent-home",
    sizeRequest: "10Gi",
    accessMode: "ReadWriteOnce",
  };
}

export function emptyVolumeProfileForm(): VolumeProfileForm {
  return {
    name: "",
    description: "",
    volumes: [emptyVolumeEntry()],
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

export function formFromVolumeProfileSpec(
  spec: Record<string, unknown>,
  name = "",
): VolumeProfileForm {
  const volumesRaw = Array.isArray(spec.volumes) ? spec.volumes : [];
  const volumes = volumesRaw.map((raw) => {
    const item = asRecord(raw);
    const size = asRecord(item.size);
    const modes = Array.isArray(item.accessModes) ? item.accessModes : [];
    return {
      name: String(item.name ?? ""),
      purpose: String(item.purpose ?? "agent-home"),
      mountPath: String(item.mountPath ?? ""),
      sizeRequest: String(size.request ?? "10Gi"),
      accessMode: modes.length ? String(modes[0]) : "ReadWriteOnce",
    };
  });
  return {
    name,
    description: String(spec.description ?? ""),
    volumes: volumes.length ? volumes : [emptyVolumeEntry()],
  };
}

export function buildVolumeProfileSpec(form: VolumeProfileForm): Record<string, unknown> {
  const volumes = form.volumes
    .filter((v) => v.name.trim())
    .map((v) => {
      const entry: Record<string, unknown> = {
        name: v.name.trim(),
        purpose: v.purpose.trim() || "agent-home",
        mountPath: v.mountPath.trim() || `/${v.name.trim()}`,
      };
      if (v.sizeRequest.trim()) {
        entry.size = { request: v.sizeRequest.trim() };
      }
      if (v.accessMode.trim()) {
        entry.accessModes = [v.accessMode.trim()];
      }
      return entry;
    });
  const spec: Record<string, unknown> = { volumes };
  if (form.description.trim()) {
    spec.description = form.description.trim();
  }
  return spec;
}

export function validateVolumeProfileForm(form: VolumeProfileForm, isCreate: boolean): string | null {
  if (isCreate && !form.name.trim()) {
    return "Name is required";
  }
  if (isCreate && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(form.name.trim())) {
    return "Name must be a DNS-1123 label";
  }
  if (form.volumes.filter((v) => v.name.trim()).length === 0) {
    return "Add at least one volume entry";
  }
  return null;
}
