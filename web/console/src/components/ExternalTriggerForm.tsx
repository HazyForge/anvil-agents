import {
  DEFAULT_WEBHOOK_SECRET_KEY,
  emptyTarget,
  type ExternalTriggerForm as ExternalTriggerFormModel,
  type ExternalTriggerTargetForm,
  type ExternalTriggerTargetKind,
} from "../pages/library/externalTriggerForm";

interface Props {
  form: ExternalTriggerFormModel;
  disabled?: boolean;
  isCreate: boolean;
  status?: Record<string, unknown> | null;
  onChange: <K extends keyof ExternalTriggerFormModel>(
    key: K,
    value: ExternalTriggerFormModel[K],
  ) => void;
}

function statusString(status: Record<string, unknown> | null | undefined, key: string): string {
  if (!status) {
    return "";
  }
  const value = status[key];
  if (value == null || value === "") {
    return "";
  }
  return String(value);
}

function statusHTTPRoute(status: Record<string, unknown> | null | undefined): Record<string, unknown> {
  if (!status) {
    return {};
  }
  const value = status.httpRoute;
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

function conditionFlag(value: unknown): string {
  if (value === true) {
    return "true";
  }
  if (value === false) {
    return "false";
  }
  return "";
}

export function ExternalTriggerForm({ form, disabled, isCreate, status, onChange }: Props) {
  function updateTarget(index: number, patch: Partial<ExternalTriggerTargetForm>) {
    const next = form.targets.map((entry, i) => (i === index ? { ...entry, ...patch } : entry));
    onChange("targets", next);
  }

  function removeTarget(index: number) {
    if (form.targets.length <= 1) {
      onChange("targets", [emptyTarget()]);
      return;
    }
    onChange(
      "targets",
      form.targets.filter((_, i) => i !== index),
    );
  }

  const phase = statusString(status, "phase");
  const webhookPath = statusString(status, "webhookPath");
  const receiverID = statusString(status, "receiverID");
  const lastDeliveryID = statusString(status, "lastDeliveryID");
  const lastDeliveryAt = statusString(status, "lastDeliveryAt");
  const lastEventType = statusString(status, "lastEventType");
  const lastError = statusString(status, "lastError");
  const deliveryCount = statusString(status, "deliveryCount");
  const httpRoute = statusHTTPRoute(status);
  const publicURL = statusString(httpRoute, "publicURL");
  const routeName = statusString(httpRoute, "name");
  const routeNamespace = statusString(httpRoute, "namespace");
  const routeRef =
    routeNamespace && routeName ? `${routeNamespace}/${routeName}` : routeName;
  const routeAccepted = conditionFlag(httpRoute.accepted);
  const routeProgrammed = conditionFlag(httpRoute.programmed);

  return (
    <div className="guided-form">
      <section className="explain-panel">
        <h3 className="explain-title">What is an AgentExternalTrigger?</h3>
        <p className="explain-body">
          An inbound <strong>GitHub webhook receiver</strong> that verifies HMAC signatures and
          creates or annotates AgentRuns for matching repositories and events. Secret bytes stay
          in-cluster — this console only references Secret name and key.
        </p>
        <ul className="explain-list">
          <li>
            <strong>Path and public URL only.</strong> Status exposes{" "}
            <span className="mono">webhookPath</span>, <span className="mono">receiverID</span>,
            and the controller-owned HTTPRoute URL — never the shared secret.
          </li>
          <li>
            <strong>Targets</strong> are same-namespace AgentRunProfile, AgentCouncil, or AgentRun
            objects.
          </li>
        </ul>
      </section>

      {!isCreate ? (
        <section className="explain-panel">
          <h3 className="explain-title">Receiver status</h3>
          <div className="chip-row" style={{ marginBottom: "0.75rem" }}>
            <span className="chip">{phase || "Pending"}</span>
            {form.suspend ? <span className="chip chip-mute">suspend</span> : null}
            {deliveryCount ? (
              <span className="chip mono">deliveries:{deliveryCount}</span>
            ) : null}
            {lastEventType ? <span className="chip mono">{lastEventType}</span> : null}
          </div>
          <label className="field">
            <span className="label">Public webhook URL</span>
            <input className="input mono" value={publicURL || "—"} disabled />
          </label>
          <label className="field">
            <span className="label">HTTPRoute</span>
            <input className="input mono" value={routeRef || "—"} disabled />
          </label>
          <div className="chip-row" style={{ marginBottom: "0.75rem" }}>
            {routeAccepted ? (
              <span className="chip mono">accepted:{routeAccepted}</span>
            ) : null}
            {routeProgrammed ? (
              <span className="chip mono">programmed:{routeProgrammed}</span>
            ) : null}
          </div>
          <label className="field">
            <span className="label">Webhook path</span>
            <input className="input mono" value={webhookPath || "—"} disabled />
          </label>
          <label className="field">
            <span className="label">Receiver ID</span>
            <input className="input mono" value={receiverID || "—"} disabled />
          </label>
          <div className="field-row">
            <label className="field">
              <span className="label">Last delivery ID</span>
              <input className="input mono" value={lastDeliveryID || "—"} disabled />
            </label>
            <label className="field">
              <span className="label">Last delivery at</span>
              <input className="input mono" value={lastDeliveryAt || "—"} disabled />
            </label>
          </div>
          <label className="field">
            <span className="label">Last event type</span>
            <input className="input mono" value={lastEventType || "—"} disabled />
          </label>
          <label className="field">
            <span className="label">Last error</span>
            <textarea className="textarea" value={lastError || "—"} disabled rows={2} />
          </label>
          <label className="field">
            <span className="label">Delivery count</span>
            <input className="input mono" value={deliveryCount || "0"} disabled />
          </label>
          <p className="field-help">
            Secret values are never returned by the API or shown here — only Secret name/key refs.
          </p>
        </section>
      ) : null}

      <label className="field">
        <span className="label">Name</span>
        <input
          className="input mono"
          value={form.name}
          disabled={!isCreate || disabled}
          onChange={(event) => onChange("name", event.target.value)}
          placeholder="github-pr-triage"
        />
      </label>

      <label className="field field-checkbox">
        <span className="checkbox-row">
          <input
            type="checkbox"
            checked={form.suspend}
            disabled={disabled}
            onChange={(event) => onChange("suspend", event.target.checked)}
          />
          <span className="label" style={{ margin: 0 }}>
            Suspend deliveries
          </span>
        </span>
        <p className="field-help">
          Detaches the public HTTPRoute and blocks new AgentRun creates/annotations.
          The receiver ID is kept.
        </p>
      </label>

      <label className="field">
        <span className="label">Public hostname override (optional)</span>
        <input
          className="input mono"
          value={form.hostnameOverride}
          disabled={disabled}
          onChange={(event) => onChange("hostnameOverride", event.target.value)}
          placeholder="hooks.example.com"
        />
        <p className="field-help">
          Exact hostname only — no wildcards. Empty uses the install HTTPRoute hostnames.
        </p>
      </label>

      <label className="field">
        <span className="label">Source kind</span>
        <select
          className="select"
          value={form.sourceKind}
          disabled={disabled}
          onChange={() => onChange("sourceKind", "githubWebhook")}
        >
          <option value="githubWebhook">githubWebhook</option>
        </select>
      </label>

      <label className="field">
        <span className="label">Repositories (owner/name, one per line)</span>
        <textarea
          className="textarea mono"
          value={form.repositoriesText}
          disabled={disabled}
          onChange={(event) => onChange("repositoriesText", event.target.value)}
          placeholder={"hazyforge/anvil-agents\nhazyforge/anvil-primaris"}
          rows={4}
        />
      </label>

      <label className="field">
        <span className="label">Events (optional, one per line)</span>
        <textarea
          className="textarea mono"
          value={form.eventsText}
          disabled={disabled}
          onChange={(event) => onChange("eventsText", event.target.value)}
          placeholder={"pull_request\nissue_comment\npush"}
          rows={3}
        />
        <p className="field-help">Empty accepts any signed GitHub event that reaches the receiver.</p>
      </label>

      <h3 className="form-section-title">Webhook Secret ref</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Reference only — never paste Secret data into this form.
      </p>
      <div className="field-row">
        <label className="field">
          <span className="label">Secret name</span>
          <input
            className="input mono"
            value={form.secretName}
            disabled={disabled}
            onChange={(event) => onChange("secretName", event.target.value)}
            placeholder="github-webhook"
          />
        </label>
        <label className="field">
          <span className="label">Webhook secret key</span>
          <input
            className="input mono"
            value={form.webhookSecretKey}
            disabled={disabled}
            onChange={(event) => onChange("webhookSecretKey", event.target.value)}
            placeholder={DEFAULT_WEBHOOK_SECRET_KEY}
          />
        </label>
      </div>
      <label className="field">
        <span className="label">Path token key (optional)</span>
        <input
          className="input mono"
          value={form.pathTokenKey}
          disabled={disabled}
          onChange={(event) => onChange("pathTokenKey", event.target.value)}
          placeholder="pathToken"
        />
        <p className="field-help">
          When set, requests must also send matching <span className="mono">X-Anvil-Trigger-Token</span>.
          Key name only — not the token value.
        </p>
      </label>

      <h3 className="form-section-title">Targets</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Same-namespace delivery destinations. Empty names are ignored on save.
      </p>
      <div className="form-entry-list">
        {form.targets.map((target, index) => (
          <div key={index} className="form-entry-card">
            <div className="form-entry-header">
              <span className="mono">#{index + 1}</span>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={disabled}
                onClick={() => removeTarget(index)}
              >
                Remove
              </button>
            </div>
            <div className="field-row">
              <label className="field">
                <span className="label">Kind</span>
                <select
                  className="select"
                  value={target.kind}
                  disabled={disabled}
                  onChange={(event) =>
                    updateTarget(index, {
                      kind: event.target.value as ExternalTriggerTargetKind,
                    })
                  }
                >
                  <option value="AgentRunProfile">AgentRunProfile</option>
                  <option value="AgentCouncil">AgentCouncil</option>
                  <option value="AgentRun">AgentRun</option>
                </select>
              </label>
              <label className="field">
                <span className="label">Name</span>
                <input
                  className="input mono"
                  value={target.name}
                  disabled={disabled}
                  onChange={(event) => updateTarget(index, { name: event.target.value })}
                  placeholder="pr-reviewer"
                />
              </label>
            </div>
            {target.kind === "AgentCouncil" ? (
              <label className="field">
                <span className="label">Council delivery</span>
                <input
                  className="input mono"
                  value={target.councilDelivery}
                  disabled={disabled}
                  onChange={(event) =>
                    updateTarget(index, { councilDelivery: event.target.value })
                  }
                  placeholder="allMembers or coordinatorRole"
                />
                <p className="field-help">
                  Empty or <span className="mono">allMembers</span> delivers to every member.
                  Any other value is treated as a coordinator role filter.
                </p>
              </label>
            ) : null}
          </div>
        ))}
      </div>
      <button
        type="button"
        className="btn btn-ghost"
        disabled={disabled}
        onClick={() => onChange("targets", [...form.targets, emptyTarget()])}
      >
        Add target
      </button>

      <label className="field">
        <span className="label">Prompt template (optional)</span>
        <textarea
          className="textarea mono"
          value={form.promptTemplate}
          disabled={disabled}
          onChange={(event) => onChange("promptTemplate", event.target.value)}
          placeholder="Event {{eventType}} on {{repository}} (delivery {{deliveryID}})\n{{summary}}"
          rows={4}
        />
        <p className="field-help">
          Placeholders:{" "}
          <span className="mono">
            {"{{eventType}} {{repository}} {{deliveryID}} {{summary}}"}
          </span>
        </p>
      </label>

      <div className="field-row">
        <label className="field">
          <span className="label">Concurrency policy</span>
          <select
            className="select"
            value={form.concurrencyPolicy}
            disabled={disabled}
            onChange={(event) =>
              onChange(
                "concurrencyPolicy",
                event.target.value as ExternalTriggerFormModel["concurrencyPolicy"],
              )
            }
          >
            <option value="">Default (Forbid)</option>
            <option value="Forbid">Forbid</option>
            <option value="Allow">Allow</option>
          </select>
        </label>
        <label className="field">
          <span className="label">Max deliveries / day (UTC)</span>
          <input
            className="input mono"
            value={form.maxDeliveriesPerDay}
            disabled={disabled}
            onChange={(event) => onChange("maxDeliveriesPerDay", event.target.value)}
            placeholder="empty = unlimited"
            inputMode="numeric"
          />
        </label>
      </div>
    </div>
  );
}
