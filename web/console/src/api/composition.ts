import { apiFetch, APIError } from "./client";
import type {
  CompositionDocument,
  CompositionListResponse,
  CompositionPathSegment,
} from "./types.composition";

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
    // non-JSON
  }
  if (code === "gitops_protected") {
    message =
      message ||
      "This object is owned by GitOps. Edit the Git source of truth instead of the live cluster.";
  } else if (code === "not_console_managed") {
    message =
      message ||
      "Only console-managed objects can be edited. GitOps and unlabeled objects stay read-only.";
  }
  return new APIError(response.status, code, message);
}

function basePath(namespace: string, segment: CompositionPathSegment, name?: string): string {
  const root = `/api/v1/namespaces/${encodeURIComponent(namespace)}/${segment}`;
  return name ? `${root}/${encodeURIComponent(name)}` : root;
}

export async function listComposition(
  token: string,
  namespace: string,
  segment: CompositionPathSegment,
  limit = 200,
  signal?: AbortSignal,
): Promise<CompositionDocument[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  const response = await apiFetch(`${basePath(namespace, segment)}?${params}`, token, { signal });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  const body = (await response.json()) as CompositionListResponse;
  return body.items ?? [];
}

export async function getComposition(
  token: string,
  namespace: string,
  segment: CompositionPathSegment,
  name: string,
  signal?: AbortSignal,
): Promise<CompositionDocument> {
  const response = await apiFetch(basePath(namespace, segment, name), token, { signal });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as CompositionDocument;
}

export async function createComposition(
  token: string,
  namespace: string,
  segment: CompositionPathSegment,
  doc: {
    metadata: {
      name: string;
      labels?: Record<string, string>;
      annotations?: Record<string, string>;
    };
    spec: Record<string, unknown>;
  },
): Promise<CompositionDocument> {
  const response = await apiFetch(basePath(namespace, segment), token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(doc),
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as CompositionDocument;
}

export async function updateComposition(
  token: string,
  namespace: string,
  segment: CompositionPathSegment,
  name: string,
  doc: {
    metadata: {
      name?: string;
      resourceVersion: string;
      labels?: Record<string, string>;
      annotations?: Record<string, string>;
    };
    spec: Record<string, unknown>;
  },
): Promise<CompositionDocument> {
  const response = await apiFetch(basePath(namespace, segment, name), token, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(doc),
  });
  if (!response.ok) {
    throw await readAPIError(response);
  }
  return (await response.json()) as CompositionDocument;
}

export async function deleteComposition(
  token: string,
  namespace: string,
  segment: CompositionPathSegment,
  name: string,
): Promise<void> {
  const response = await apiFetch(basePath(namespace, segment, name), token, {
    method: "DELETE",
  });
  if (!response.ok && response.status !== 204) {
    throw await readAPIError(response);
  }
}
