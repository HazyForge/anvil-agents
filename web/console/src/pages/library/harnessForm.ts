/** Form model + conversion for AgentHarnessProfile console editing. */

export type HarnessBackendKind =
  | "codex"
  | "openCode"
  | "hermesAgent"
  | "openClaw"
  | "grokBuild"
  | "piAgent"
  | "custom";

export interface BackendKindOption {
  kind: HarnessBackendKind;
  title: string;
  summary: string;
  detail: string;
  needsSharedProvider?: boolean;
  modelHint?: string;
  defaultModel?: string;
}

export const BACKEND_KIND_OPTIONS: BackendKindOption[] = [
  {
    kind: "codex",
    title: "Codex",
    summary: "OpenAI Codex CLI runner",
    detail: "Good default for repository work with OpenAI models and sandbox modes.",
    modelHint: "Optional model override (empty uses harness defaults)",
  },
  {
    kind: "openCode",
    title: "OpenCode",
    summary: "Provider-native OpenCode CLI",
    detail: "Pick a provider/model like openai/gpt-5.4 or opencode/big-pickle. Auth is provider-native.",
    modelHint: "provider/model, e.g. openai/gpt-5.4 or opencode/big-pickle",
    defaultModel: "opencode/big-pickle",
  },
  {
    kind: "grokBuild",
    title: "Grok Build",
    summary: "xAI Grok Build adapter",
    detail: "xAI models with optional durable Grok home via AgentDataVolume.",
    needsSharedProvider: true,
    modelHint: "e.g. grok-4.5",
    defaultModel: "grok-4.5",
  },
  {
    kind: "hermesAgent",
    title: "Hermes Agent",
    summary: "Hermes multi-provider agent",
    detail: "Uses shared modelProvider + providerAuthMode plus a Hermes model string.",
    needsSharedProvider: true,
    modelHint: "Hermes model id",
  },
  {
    kind: "openClaw",
    title: "OpenClaw",
    summary: "OpenClaw agent runtime",
    detail: "Provider-aware OpenClaw adapter with model and thinking settings.",
    needsSharedProvider: true,
    modelHint: "OpenClaw model id",
  },
  {
    kind: "piAgent",
    title: "Pi Agent",
    summary: "Pi coding agent",
    detail: "Pi coding agent with provider, model, and mode settings.",
    needsSharedProvider: true,
    modelHint: "Pi model id",
  },
  {
    kind: "custom",
    title: "Custom image",
    summary: "Operator-owned container",
    detail: "Bring your own image and command. Image is required.",
    modelHint: "Not used for custom — set command/args in advanced if needed",
  },
];

export interface HarnessForm {
  name: string;
  description: string;
  backendKind: HarnessBackendKind;
  image: string;
  modelProvider: string;
  providerAuthMode: string;
  model: string;
  codexSandbox: string;
  openCodeAuto: boolean;
  openCodePure: boolean;
  openCodeFormat: string;
  serviceAccountName: string;
  envSecretNames: string[];
  dataVolumeNames: string[];
  imagePullSecretNames: string[];
  workdir: string;
  timeoutSeconds: string;
  ttlSecondsAfterFinished: string;
  cpuRequest: string;
  memoryRequest: string;
  cpuLimit: string;
  memoryLimit: string;
}

export function emptyHarnessForm(): HarnessForm {
  return {
    name: "",
    description: "",
    backendKind: "codex",
    image: "",
    modelProvider: "",
    providerAuthMode: "",
    model: "",
    codexSandbox: "read-only",
    openCodeAuto: false,
    openCodePure: true,
    openCodeFormat: "json",
    serviceAccountName: "",
    envSecretNames: [],
    dataVolumeNames: [],
    imagePullSecretNames: [],
    workdir: "/workspace",
    timeoutSeconds: "1800",
    ttlSecondsAfterFinished: "86400",
    cpuRequest: "100m",
    memoryRequest: "256Mi",
    cpuLimit: "2",
    memoryLimit: "2Gi",
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

function refNames(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => {
      if (typeof item === "string") {
        return item.trim();
      }
      if (typeof item === "object" && item && "name" in item) {
        return String((item as { name?: string }).name ?? "").trim();
      }
      return "";
    })
    .filter(Boolean);
}

function resourceQuantity(
  resources: Record<string, unknown>,
  side: "requests" | "limits",
  key: string,
): string {
  const block = asRecord(resources[side]);
  const value = block[key];
  return value == null ? "" : String(value);
}

export function formFromHarnessSpec(spec: Record<string, unknown>, name = ""): HarnessForm {
  const backend = asRecord(spec.backend);
  const execution = asRecord(spec.execution);
  const resources = asRecord(execution.resources);
  const kind = String(backend.kind ?? "codex") as HarnessBackendKind;
  const codex = asRecord(backend.codex);
  const openCode = asRecord(backend.openCode);
  const hermes = asRecord(backend.hermesAgent);
  const openClaw = asRecord(backend.openClaw);
  const grokBuild = asRecord(backend.grokBuild);
  const pi = asRecord(backend.piAgent);

  let model = "";
  switch (kind) {
    case "codex":
      model = String(codex.model ?? "");
      break;
    case "openCode":
      model = String(openCode.model ?? "");
      break;
    case "hermesAgent":
      model = String(hermes.model ?? "");
      break;
    case "openClaw":
      model = String(openClaw.model ?? "");
      break;
    case "grokBuild":
      model = String(grokBuild.model ?? "");
      break;
    case "piAgent":
      model = String(pi.model ?? "");
      break;
    default:
      model = "";
  }

  const form = emptyHarnessForm();
  form.name = name;
  form.description = String(spec.description ?? "");
  form.backendKind = BACKEND_KIND_OPTIONS.some((opt) => opt.kind === kind) ? kind : "codex";
  form.image = String(backend.image ?? "");
  form.modelProvider = String(backend.modelProvider ?? "");
  form.providerAuthMode = String(backend.providerAuthMode ?? "");
  form.model = model;
  form.codexSandbox = String(codex.sandbox ?? "read-only");
  form.openCodeAuto = Boolean(openCode.auto);
  form.openCodePure = openCode.pure === undefined ? true : Boolean(openCode.pure);
  form.openCodeFormat = String(openCode.format ?? "json");
  form.serviceAccountName = String(execution.serviceAccountName ?? "");
  form.envSecretNames = refNames(execution.envSecretRefs);
  form.dataVolumeNames = refNames(execution.dataVolumeRefs);
  form.imagePullSecretNames = refNames(execution.imagePullSecrets);
  form.workdir = String(execution.workdir ?? "/workspace");
  form.timeoutSeconds =
    execution.timeoutSeconds == null ? "" : String(execution.timeoutSeconds);
  form.ttlSecondsAfterFinished =
    execution.ttlSecondsAfterFinished == null
      ? ""
      : String(execution.ttlSecondsAfterFinished);
  form.cpuRequest = resourceQuantity(resources, "requests", "cpu");
  form.memoryRequest = resourceQuantity(resources, "requests", "memory");
  form.cpuLimit = resourceQuantity(resources, "limits", "cpu");
  form.memoryLimit = resourceQuantity(resources, "limits", "memory");
  return form;
}

function setIf(target: Record<string, unknown>, key: string, value: string | number | boolean | undefined | null) {
  if (value === undefined || value === null) {
    return;
  }
  if (typeof value === "string" && !value.trim()) {
    return;
  }
  target[key] = typeof value === "string" ? value.trim() : value;
}

function parseIntField(raw: string): number | undefined {
  const trimmed = raw.trim();
  if (!trimmed) {
    return undefined;
  }
  const n = Number(trimmed);
  if (!Number.isFinite(n) || n < 0) {
    return undefined;
  }
  return Math.floor(n);
}

export function buildHarnessSpec(form: HarnessForm): Record<string, unknown> {
  const backend: Record<string, unknown> = {
    kind: form.backendKind,
  };
  setIf(backend, "image", form.image);
  if (form.backendKind !== "openCode" && form.backendKind !== "custom") {
    setIf(backend, "modelProvider", form.modelProvider);
    setIf(backend, "providerAuthMode", form.providerAuthMode);
  }

  switch (form.backendKind) {
    case "codex": {
      const codex: Record<string, unknown> = {};
      setIf(codex, "sandbox", form.codexSandbox);
      setIf(codex, "model", form.model);
      if (Object.keys(codex).length) {
        backend.codex = codex;
      }
      break;
    }
    case "openCode": {
      const openCode: Record<string, unknown> = {
        auto: form.openCodeAuto,
        pure: form.openCodePure,
      };
      setIf(openCode, "model", form.model);
      setIf(openCode, "format", form.openCodeFormat || "json");
      backend.openCode = openCode;
      break;
    }
    case "hermesAgent": {
      const hermes: Record<string, unknown> = {};
      setIf(hermes, "model", form.model);
      if (Object.keys(hermes).length) {
        backend.hermesAgent = hermes;
      }
      break;
    }
    case "openClaw": {
      const openClaw: Record<string, unknown> = {};
      setIf(openClaw, "model", form.model);
      if (Object.keys(openClaw).length) {
        backend.openClaw = openClaw;
      }
      break;
    }
    case "grokBuild": {
      const grokBuild: Record<string, unknown> = {};
      setIf(grokBuild, "model", form.model);
      if (Object.keys(grokBuild).length) {
        backend.grokBuild = grokBuild;
      }
      if (!form.modelProvider) {
        backend.modelProvider = "xai";
      }
      break;
    }
    case "piAgent": {
      const pi: Record<string, unknown> = {};
      setIf(pi, "model", form.model);
      if (Object.keys(pi).length) {
        backend.piAgent = pi;
      }
      break;
    }
    case "custom": {
      // Image is required for custom; validated in the UI.
      break;
    }
  }

  const execution: Record<string, unknown> = {};
  setIf(execution, "serviceAccountName", form.serviceAccountName);
  setIf(execution, "workdir", form.workdir);
  const timeout = parseIntField(form.timeoutSeconds);
  if (timeout !== undefined) {
    execution.timeoutSeconds = timeout;
  }
  const ttl = parseIntField(form.ttlSecondsAfterFinished);
  if (ttl !== undefined) {
    execution.ttlSecondsAfterFinished = ttl;
  }
  if (form.envSecretNames.length) {
    execution.envSecretRefs = form.envSecretNames.map((name) => ({ name }));
  }
  if (form.dataVolumeNames.length) {
    execution.dataVolumeRefs = form.dataVolumeNames.map((name) => ({ name }));
  }
  if (form.imagePullSecretNames.length) {
    execution.imagePullSecrets = form.imagePullSecretNames.map((name) => ({ name }));
  }

  const requests: Record<string, string> = {};
  const limits: Record<string, string> = {};
  if (form.cpuRequest.trim()) {
    requests.cpu = form.cpuRequest.trim();
  }
  if (form.memoryRequest.trim()) {
    requests.memory = form.memoryRequest.trim();
  }
  if (form.cpuLimit.trim()) {
    limits.cpu = form.cpuLimit.trim();
  }
  if (form.memoryLimit.trim()) {
    limits.memory = form.memoryLimit.trim();
  }
  if (Object.keys(requests).length || Object.keys(limits).length) {
    const resources: Record<string, unknown> = {};
    if (Object.keys(requests).length) {
      resources.requests = requests;
    }
    if (Object.keys(limits).length) {
      resources.limits = limits;
    }
    execution.resources = resources;
  }

  const spec: Record<string, unknown> = {
    backend,
  };
  setIf(spec, "description", form.description);
  if (Object.keys(execution).length) {
    spec.execution = execution;
  }
  return spec;
}

export function validateHarnessForm(form: HarnessForm, isCreate: boolean): string | null {
  if (isCreate && !form.name.trim()) {
    return "Name is required";
  }
  if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(form.name.trim()) && isCreate) {
    return "Name must be a DNS-1123 label (lowercase alphanumeric and '-')";
  }
  if (form.backendKind === "custom" && !form.image.trim()) {
    return "Custom backend requires a container image";
  }
  if (form.timeoutSeconds.trim() && parseIntField(form.timeoutSeconds) === undefined) {
    return "Timeout must be a non-negative integer (seconds)";
  }
  return null;
}
