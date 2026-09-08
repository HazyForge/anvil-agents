export type ExternalTriggerTargetKind = "AgentRunProfile" | "AgentCouncil" | "AgentRun";

export interface ExternalTriggerTargetForm {
  kind: ExternalTriggerTargetKind;
  name: string;
  councilDelivery: string;
}

export interface ExternalTriggerForm {
  name: string;
  suspend: boolean;
  sourceKind: "githubWebhook";
  repositoriesText: string;
  eventsText: string;
  secretName: string;
  webhookSecretKey: string;
  pathTokenKey: string;
  hostnameOverride: string;
  targets: ExternalTriggerTargetForm[];
  promptTemplate: string;
  concurrencyPolicy: "" | "Forbid" | "Allow";
  maxDeliveriesPerDay: string;
}

export const DEFAULT_WEBHOOK_SECRET_KEY = "webhookSecret";

export function emptyTarget(): ExternalTriggerTargetForm {
  return { kind: "AgentRunProfile", name: "", councilDelivery: "" };
}

export function emptyExternalTriggerForm(): ExternalTriggerForm {
  return {
    name: "",
    suspend: false,
    sourceKind: "githubWebhook",
    repositoriesText: "",
    eventsText: "",
    secretName: "",
    webhookSecretKey: DEFAULT_WEBHOOK_SECRET_KEY,
    pathTokenKey: "",
    hostnameOverride: "",
    targets: [emptyTarget()],
    promptTemplate: "",
    concurrencyPolicy: "",
    maxDeliveriesPerDay: "",
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

function linesToList(text: string): string[] {
  return text
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function listToLines(value: unknown): string {
  if (!Array.isArray(value)) {
    return "";
  }
  return value.map((item) => String(item ?? "").trim()).filter(Boolean).join("\n");
}

export function formFromExternalTriggerSpec(
  spec: Record<string, unknown>,
  name = "",
): ExternalTriggerForm {
  const source = asRecord(spec.source);
  const github = asRecord(source.github);
  const secretRef = asRecord(spec.secretRef);
  const targetsRaw = Array.isArray(spec.targets) ? spec.targets : [];
  const targets = targetsRaw.map((raw) => {
    const item = asRecord(raw);
    const kindRaw = String(item.kind ?? "AgentRunProfile");
    const kind: ExternalTriggerTargetKind =
      kindRaw === "AgentCouncil" || kindRaw === "AgentRun" ? kindRaw : "AgentRunProfile";
    return {
      kind,
      name: String(item.name ?? ""),
      councilDelivery: String(item.councilDelivery ?? ""),
    };
  });
  const httpRoute = asRecord(spec.httpRoute);
  const concurrency = String(spec.concurrencyPolicy ?? "");
  const maxDeliveries =
    typeof spec.maxDeliveriesPerDay === "number" && Number.isFinite(spec.maxDeliveriesPerDay)
      ? String(spec.maxDeliveriesPerDay)
      : String(spec.maxDeliveriesPerDay ?? "").trim();

  return {
    name,
    suspend: Boolean(spec.suspend),
    sourceKind: "githubWebhook",
    repositoriesText: listToLines(github.repositories),
    eventsText: listToLines(github.events),
    secretName: String(secretRef.name ?? ""),
    webhookSecretKey: String(secretRef.webhookSecretKey ?? "").trim() || DEFAULT_WEBHOOK_SECRET_KEY,
    pathTokenKey: String(secretRef.pathTokenKey ?? ""),
    hostnameOverride: String(httpRoute.hostname ?? ""),
    targets: targets.length ? targets : [emptyTarget()],
    promptTemplate: String(spec.promptTemplate ?? ""),
    concurrencyPolicy: concurrency === "Allow" || concurrency === "Forbid" ? concurrency : "",
    maxDeliveriesPerDay: maxDeliveries,
  };
}

export function buildExternalTriggerSpec(form: ExternalTriggerForm): Record<string, unknown> {
  const repositories = linesToList(form.repositoriesText);
  const events = linesToList(form.eventsText);
  const targets = form.targets
    .filter((target) => target.name.trim())
    .map((target) => {
      const entry: Record<string, unknown> = {
        kind: target.kind,
        name: target.name.trim(),
      };
      if (target.kind === "AgentCouncil" && target.councilDelivery.trim()) {
        entry.councilDelivery = target.councilDelivery.trim();
      }
      return entry;
    });

  const secretRef: Record<string, unknown> = {
    name: form.secretName.trim(),
  };
  const secretKey = form.webhookSecretKey.trim() || DEFAULT_WEBHOOK_SECRET_KEY;
  if (secretKey !== DEFAULT_WEBHOOK_SECRET_KEY) {
    secretRef.webhookSecretKey = secretKey;
  }
  if (form.pathTokenKey.trim()) {
    secretRef.pathTokenKey = form.pathTokenKey.trim();
  }

  const github: Record<string, unknown> = { repositories };
  if (events.length) {
    github.events = events;
  }

  const spec: Record<string, unknown> = {
    source: {
      kind: "githubWebhook",
      github,
    },
    secretRef,
    targets,
  };

  if (form.suspend) {
    spec.suspend = true;
  }
  if (form.hostnameOverride.trim()) {
    spec.httpRoute = { hostname: form.hostnameOverride.trim() };
  }
  if (form.promptTemplate.trim()) {
    spec.promptTemplate = form.promptTemplate;
  }
  if (form.concurrencyPolicy) {
    spec.concurrencyPolicy = form.concurrencyPolicy;
  }
  const maxRaw = form.maxDeliveriesPerDay.trim();
  if (maxRaw !== "") {
    const max = Number(maxRaw);
    if (Number.isFinite(max) && max >= 0) {
      spec.maxDeliveriesPerDay = Math.floor(max);
    }
  }
  return spec;
}

export function validateExternalTriggerForm(
  form: ExternalTriggerForm,
  isCreate: boolean,
): string | null {
  if (isCreate && !form.name.trim()) {
    return "Name is required";
  }
  if (isCreate && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(form.name.trim())) {
    return "Name must be a DNS-1123 label";
  }
  if (!form.secretName.trim()) {
    return "Secret name is required (Secret values are never shown)";
  }
  const hostname = form.hostnameOverride.trim();
  if (hostname) {
    if (hostname.includes("*") || !/^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$/.test(hostname)) {
      return "Public hostname override must be an exact hostname, never a wildcard";
    }
  }
  const repositories = linesToList(form.repositoriesText);
  if (repositories.length === 0) {
    return "At least one repository (owner/name) is required";
  }
  const targets = form.targets.filter((target) => target.name.trim());
  if (targets.length === 0) {
    return "At least one delivery target is required";
  }
  const maxRaw = form.maxDeliveriesPerDay.trim();
  if (maxRaw !== "") {
    const max = Number(maxRaw);
    if (!Number.isFinite(max) || max < 0 || !Number.isInteger(max)) {
      return "Max deliveries per day must be a non-negative integer (or empty)";
    }
  }
  return null;
}
