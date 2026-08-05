import { apiFetch, APIError } from "./client";
import type { ControlListResponse, ControlView, ControlWriteRequest } from "./types";

async function readAPIError(response: Response): Promise<APIError> {
  let code = `http_${response.status}`;
  let message = response.statusText || `HTTP ${response.status}`;
  try {
    const body = (await response.json()) as { error?: { code?: string; message?: string } };
    if (body.error?.code) {
      code = body.error.code;
    }
    if (body.error?.message) {
      message = body.error.message;
    }
  } catch {
    // non-JSON body
  }
  if (code === "gitops_protected") {
    message =
      message ||
      "This launch gate is owned by GitOps. Edit the Git source of truth instead of the live cluster.";
  } else if (code === "conflict") {
    message = message || "Launch gate changed since it was loaded — refresh and retry.";
  } else if (code === "controls_write_disabled") {
    message = message || "Launch gate writes are disabled on this API.";
  }
  return new APIError(response.status, code, message);
}

export async function listControls(
  token: string,
  signal?: AbortSignal,
): Promise<ControlView[]> {
  const response = await apiFetch("/api/v1/controls", token, { signal });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  const body = (await response.json()) as ControlListResponse;
  return body.items ?? [];
}

export async function updateControl(
  token: string,
  name: string,
  request: ControlWriteRequest,
): Promise<ControlView> {
  const response = await apiFetch(`/api/v1/controls/${encodeURIComponent(name)}`, token, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as ControlView;
}
