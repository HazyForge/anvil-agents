import { useMemo } from "react";
import {
  BACKEND_KIND_OPTIONS,
  type HarnessForm,
  type HarnessBackendKind,
} from "../pages/library/harnessForm";
import { StringChipList } from "./StringChipList";

interface Props {
  form: HarnessForm;
  disabled?: boolean;
  isCreate: boolean;
  dataVolumeNames: string[];
  onChange: <K extends keyof HarnessForm>(key: K, value: HarnessForm[K]) => void;
}

export function HarnessProfileForm({
  form,
  disabled,
  isCreate,
  dataVolumeNames,
  onChange,
}: Props) {
  const kindMeta = useMemo(
    () => BACKEND_KIND_OPTIONS.find((opt) => opt.kind === form.backendKind),
    [form.backendKind],
  );

  function selectKind(kind: HarnessBackendKind) {
    if (disabled) {
      return;
    }
    onChange("backendKind", kind);
    const opt = BACKEND_KIND_OPTIONS.find((item) => item.kind === kind);
    if (opt?.defaultModel && !form.model) {
      onChange("model", opt.defaultModel);
    }
    if (kind === "grokBuild" && !form.modelProvider) {
      onChange("modelProvider", "xai");
    }
    if (kind === "grokBuild" && !form.providerAuthMode) {
      onChange("providerAuthMode", "apiKey");
    }
  }

  return (
    <div className="harness-form">
      <section className="explain-panel">
        <h3 className="explain-title">What is an AgentHarnessProfile?</h3>
        <p className="explain-body">
          A harness profile is the <strong>runtime machine</strong> an agent run uses — which CLI
          adapter starts (Codex, OpenCode, Grok Build, …), which container image, which Kubernetes
          ServiceAccount and Secrets, durable volume homes, CPU/memory, and timeout.
        </p>
        <ul className="explain-list">
          <li>
            <strong>Not the role.</strong> Skills, tools, intent, and policy live on{" "}
            <span className="mono">AgentRunProfile</span>. Swap harnesses without rewriting the role.
          </li>
          <li>
            <strong>Code-execution authority.</strong> Image, ServiceAccount, and secret refs define
            what the pod can do. Prefer GitOps for production harnesses.
          </li>
          <li>
            <strong>One run = one harness.</strong> Profiles compose by reference; the Job uses
            exactly one selected harness profile (plus optional overrides).
          </li>
        </ul>
      </section>

      <label className="field">
        <span className="label">Name</span>
        <input
          className="input mono"
          value={form.name}
          disabled={!isCreate || disabled}
          onChange={(event) => onChange("name", event.target.value)}
          placeholder="codex-standard"
        />
      </label>

      <label className="field">
        <span className="label">Description</span>
        <textarea
          className="textarea"
          value={form.description}
          disabled={disabled}
          onChange={(event) => onChange("description", event.target.value)}
          placeholder="What this runtime is for (review lane, PR writer, smoke test…)"
        />
      </label>

      <div className="field">
        <span className="label">Backend runtime</span>
        <p className="field-help">
          Choose the adapter that executes AgentRuns. Cluster install maps each kind to a default
          runner image unless you override the image below.
        </p>
        <div className="backend-kind-grid">
          {BACKEND_KIND_OPTIONS.map((opt) => {
            const selected = form.backendKind === opt.kind;
            return (
              <button
                key={opt.kind}
                type="button"
                className={["backend-kind-card", selected ? "backend-kind-card-selected" : ""]
                  .filter(Boolean)
                  .join(" ")}
                disabled={disabled}
                onClick={() => selectKind(opt.kind)}
              >
                <span className="backend-kind-title">{opt.title}</span>
                <span className="backend-kind-summary">{opt.summary}</span>
                <span className="backend-kind-detail">{opt.detail}</span>
                <span className="mono backend-kind-id">{opt.kind}</span>
              </button>
            );
          })}
        </div>
      </div>

      {kindMeta ? (
        <div className="banner banner-info harness-kind-banner">
          <strong>{kindMeta.title}</strong> — {kindMeta.detail}
        </div>
      ) : null}

      <div className="field-row">
        {form.backendKind === "codex" ? (
          <label className="field">
            <span className="label">Codex sandbox</span>
            <select
              className="select"
              value={form.codexSandbox}
              disabled={disabled}
              onChange={(event) => onChange("codexSandbox", event.target.value)}
            >
              <option value="read-only">read-only</option>
              <option value="workspace-write">workspace-write</option>
              <option value="danger-full-access">danger-full-access</option>
            </select>
            <p className="field-help">
              Hardened nodes may block unprivileged user namespaces required by some sandbox modes.
            </p>
          </label>
        ) : null}

        {form.backendKind !== "custom" ? (
          <label className="field">
            <span className="label">Model</span>
            <input
              className="input mono"
              value={form.model}
              disabled={disabled}
              onChange={(event) => onChange("model", event.target.value)}
              placeholder={kindMeta?.modelHint ?? "optional"}
            />
          </label>
        ) : null}
      </div>

      {form.backendKind === "openCode" ? (
        <div className="field-row">
          <label className="field">
            <span className="label">OpenCode format</span>
            <select
              className="select"
              value={form.openCodeFormat}
              disabled={disabled}
              onChange={(event) => onChange("openCodeFormat", event.target.value)}
            >
              <option value="json">json</option>
              <option value="default">default</option>
            </select>
          </label>
          <label className="field checkbox-field">
            <span className="label">Flags</span>
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={form.openCodePure}
                disabled={disabled}
                onChange={(event) => onChange("openCodePure", event.target.checked)}
              />
              <span>pure (disable external plugins)</span>
            </label>
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={form.openCodeAuto}
                disabled={disabled}
                onChange={(event) => onChange("openCodeAuto", event.target.checked)}
              />
              <span>auto (approve tool permissions — use carefully)</span>
            </label>
          </label>
        </div>
      ) : null}

      {kindMeta?.needsSharedProvider ? (
        <div className="field-row">
          <label className="field">
            <span className="label">Model provider</span>
            <select
              className="select"
              value={form.modelProvider}
              disabled={disabled}
              onChange={(event) => onChange("modelProvider", event.target.value)}
            >
              <option value="">(unset)</option>
              <option value="openai-codex">openai-codex</option>
              <option value="openai">openai</option>
              <option value="xai">xai</option>
            </select>
          </label>
          <label className="field">
            <span className="label">Provider auth mode</span>
            <select
              className="select"
              value={form.providerAuthMode}
              disabled={disabled}
              onChange={(event) => onChange("providerAuthMode", event.target.value)}
            >
              <option value="">(unset)</option>
              <option value="apiKey">apiKey</option>
              <option value="oauth">oauth</option>
            </select>
          </label>
        </div>
      ) : null}

      <label className="field">
        <span className="label">
          Container image {form.backendKind === "custom" ? "(required)" : "(optional override)"}
        </span>
        <input
          className="input mono"
          value={form.image}
          disabled={disabled}
          onChange={(event) => onChange("image", event.target.value)}
          placeholder={
            form.backendKind === "custom"
              ? "ghcr.io/example/my-agent:tag-or-digest"
              : "empty = cluster default for this backend kind"
          }
        />
      </label>

      <h3 className="form-section-title">Execution envelope</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Identity, credentials, storage, and sizing for the AgentRun Job. Secrets are projected via{" "}
        <span className="mono">envFrom</span> — never put credentials in the description.
      </p>

      <div className="field-row">
        <label className="field">
          <span className="label">ServiceAccount</span>
          <input
            className="input mono"
            value={form.serviceAccountName}
            disabled={disabled}
            onChange={(event) => onChange("serviceAccountName", event.target.value)}
            placeholder="agent-runner"
          />
        </label>
        <label className="field">
          <span className="label">Workdir</span>
          <input
            className="input mono"
            value={form.workdir}
            disabled={disabled}
            onChange={(event) => onChange("workdir", event.target.value)}
            placeholder="/workspace"
          />
        </label>
      </div>

      <div className="field-row">
        <label className="field">
          <span className="label">Timeout (seconds)</span>
          <input
            className="input mono"
            value={form.timeoutSeconds}
            disabled={disabled}
            onChange={(event) => onChange("timeoutSeconds", event.target.value)}
            placeholder="1800"
          />
        </label>
        <label className="field">
          <span className="label">Job TTL after finish (seconds)</span>
          <input
            className="input mono"
            value={form.ttlSecondsAfterFinished}
            disabled={disabled}
            onChange={(event) => onChange("ttlSecondsAfterFinished", event.target.value)}
            placeholder="86400"
          />
        </label>
      </div>

      <StringChipList
        label="Credential Secrets (envFrom)"
        help="Same-namespace Secret names projected into the agent container."
        value={form.envSecretNames}
        disabled={disabled}
        placeholder="e.g. codex-credentials"
        onChange={(next) => onChange("envSecretNames", next)}
      />

      <StringChipList
        label="Data volumes (durable homes)"
        help="AgentDataVolume names for sessions and OAuth homes. Tool acquisition caching is selected separately below."
        value={form.dataVolumeNames}
        disabled={disabled}
        suggestions={dataVolumeNames}
        placeholder="e.g. my-agent-grok-home"
        onChange={(next) => onChange("dataVolumeNames", next)}
      />

      <label className="field">
        <span className="label">Structured tool cache (optional)</span>
        <select className="select" value={form.toolCacheVolumeName} disabled={disabled} onChange={(event) => onChange("toolCacheVolumeName", event.target.value)}>
          <option value="">Per-run ephemeral emptyDir</option>
          {dataVolumeNames.map((name) => <option key={name} value={name}>{name}</option>)}
        </select>
        <p className="field-help">Dedicated AgentDataVolume for content-addressed structured tool artifacts. Never reuse a model authentication home.</p>
      </label>

      <StringChipList
        label="Image pull secrets"
        help="Optional registry pull credentials in this namespace."
        value={form.imagePullSecretNames}
        disabled={disabled}
        placeholder="e.g. ghcr-creds"
        onChange={(next) => onChange("imagePullSecretNames", next)}
      />

      <h3 className="form-section-title">Resources</h3>
      <div className="field-row">
        <label className="field">
          <span className="label">CPU request</span>
          <input
            className="input mono"
            value={form.cpuRequest}
            disabled={disabled}
            onChange={(event) => onChange("cpuRequest", event.target.value)}
            placeholder="100m"
          />
        </label>
        <label className="field">
          <span className="label">Memory request</span>
          <input
            className="input mono"
            value={form.memoryRequest}
            disabled={disabled}
            onChange={(event) => onChange("memoryRequest", event.target.value)}
            placeholder="256Mi"
          />
        </label>
      </div>
      <div className="field-row">
        <label className="field">
          <span className="label">CPU limit</span>
          <input
            className="input mono"
            value={form.cpuLimit}
            disabled={disabled}
            onChange={(event) => onChange("cpuLimit", event.target.value)}
            placeholder="2"
          />
        </label>
        <label className="field">
          <span className="label">Memory limit</span>
          <input
            className="input mono"
            value={form.memoryLimit}
            disabled={disabled}
            onChange={(event) => onChange("memoryLimit", event.target.value)}
            placeholder="2Gi"
          />
        </label>
      </div>
    </div>
  );
}
