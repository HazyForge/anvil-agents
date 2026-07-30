export type SourceMode = "inline" | "github";
export type ToolSourceMode = "httpArtifact" | "ociArtifact" | "inlineScript" | "setupScript";
export type MCPTransportMode = "stdio" | "streamableHTTP";

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.map(String) : [];
}

function json(value: unknown, fallback: unknown): string {
  return JSON.stringify(value ?? fallback, null, 2);
}

function parseJSON<T>(raw: string, label: string): T {
  try {
    return JSON.parse(raw) as T;
  } catch (error) {
    throw new Error(`${label} must be valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function dnsName(name: string, isCreate: boolean): string | null {
  if (isCreate && !name.trim()) return "Name is required";
  if (isCreate && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name.trim())) return "Name must be a DNS-1123 label";
  return null;
}

export interface SkillForm {
  name: string;
  description: string;
  mode: SourceMode;
  skillMD: string;
  referencesJSON: string;
  repository: string;
  ref: string;
  path: string;
  referencePaths: string[];
  apiBaseURL: string;
}

export function emptySkillForm(): SkillForm {
  return { name: "", description: "", mode: "inline", skillMD: "", referencesJSON: "[]", repository: "", ref: "", path: "SKILL.md", referencePaths: [], apiBaseURL: "" };
}

export function formFromSkillSpec(spec: Record<string, unknown>, name = ""): SkillForm {
  const form = emptySkillForm();
  const inline = asRecord(spec.inline);
  const github = asRecord(spec.github);
  return {
    ...form,
    name,
    description: String(spec.description ?? ""),
    mode: spec.github ? "github" : "inline",
    skillMD: String(inline.skillMD ?? ""),
    referencesJSON: json(inline.references, []),
    repository: String(github.repository ?? ""),
    ref: String(github.ref ?? ""),
    path: String(github.path ?? "SKILL.md"),
    referencePaths: strings(github.referencePaths),
    apiBaseURL: String(github.apiBaseURL ?? ""),
  };
}

export function buildSkillSpec(form: SkillForm): Record<string, unknown> {
  const spec: Record<string, unknown> = {};
  if (form.description.trim()) spec.description = form.description.trim();
  if (form.mode === "inline") {
    const references = parseJSON<unknown[]>(form.referencesJSON || "[]", "References");
    spec.inline = { skillMD: form.skillMD, ...(references.length ? { references } : {}) };
  } else {
    spec.github = {
      repository: form.repository.trim(), ref: form.ref.trim(), path: form.path.trim(),
      ...(form.referencePaths.length ? { referencePaths: form.referencePaths } : {}),
      ...(form.apiBaseURL.trim() ? { apiBaseURL: form.apiBaseURL.trim() } : {}),
    };
  }
  return spec;
}

export function validateSkillForm(form: SkillForm, isCreate: boolean): string | null {
  const base = dnsName(form.name, isCreate); if (base) return base;
  if (form.mode === "inline" && !form.skillMD.trim()) return "SKILL.md is required";
  if (form.mode === "github" && (!form.repository.trim() || !/^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/.test(form.ref.trim()) || !form.path.trim())) return "GitHub repository, immutable 40/64-character commit, and SKILL.md path are required";
	if (form.mode === "inline") { try { parseJSON(form.referencesJSON || "[]", "References"); } catch (error) { return String(error); } }
  return null;
}

export interface GitHubSkillPackagePreview {
  referencePaths: string[];
  ignoredPaths: string[];
}

interface PreviewFetchResponse {
  ok: boolean;
  status: number;
  json: () => Promise<unknown>;
}

export async function previewGitHubSkillPackage(
  form: SkillForm,
  fetcher: (input: string) => Promise<PreviewFetchResponse> = fetch,
): Promise<GitHubSkillPackagePreview> {
  if (form.mode !== "github") throw new Error("Select a GitHub source before previewing a package");
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(form.repository.trim())) throw new Error("Repository must be owner/name");
  if (!/^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/.test(form.ref.trim())) throw new Error("A full immutable commit SHA is required");
  const skillPath = form.path.trim();
  if (!(skillPath === "SKILL.md" || skillPath.endsWith("/SKILL.md"))) throw new Error("Path must name SKILL.md");

  const base = new URL(form.apiBaseURL.trim() || "https://api.github.com");
  if (base.protocol !== "https:" || base.username || base.password || base.search || base.hash) throw new Error("GitHub API base URL must be credential-free HTTPS");
  if (base.origin !== "https://api.github.com") throw new Error("Browser import is limited to public github.com packages; author enterprise references manually");
  const [owner, repository] = form.repository.trim().split("/");
  const root = skillPath.slice(0, -"SKILL.md".length);
  base.pathname = `${base.pathname.replace(/\/$/, "")}/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repository)}/git/trees/${encodeURIComponent(form.ref.trim())}`;
  base.searchParams.set("recursive", "1");
  const response = await fetcher(base.toString());
  if (!response.ok) throw new Error(`GitHub package preview failed with HTTP ${response.status}`);
  const payload = asRecord(await response.json());
  if (payload.truncated === true) throw new Error("GitHub returned a truncated repository tree; narrow the package path or author reference paths manually");
  const entries = Array.isArray(payload.tree) ? payload.tree.map(asRecord) : [];
  const blobs = entries
    .filter((entry) => entry.type === "blob")
    .map((entry) => String(entry.path ?? ""))
    .filter((path) => path === skillPath || path.startsWith(root));
  if (!blobs.includes(skillPath)) throw new Error(`${skillPath} was not found at the pinned commit`);
  const referencePaths: string[] = [];
  const ignoredPaths: string[] = [];
  for (const path of blobs) {
    if (path === skillPath) continue;
    const safeMarkdown = path.endsWith(".md") && path !== "SKILL.md" && !path.endsWith("/SKILL.md") && path.split("/").every((part) => part && part !== "." && part !== "..");
    if (safeMarkdown) referencePaths.push(path);
    else ignoredPaths.push(path);
  }
  return { referencePaths: referencePaths.sort(), ignoredPaths: ignoredPaths.sort() };
}

export interface ToolForm {
  name: string; description: string; executableName: string; executablePath: string;
  mode: ToolSourceMode; artifactsJSON: string; interpreter: string[]; script: string;
  setupScript: string; verifyCommand: string[];
}

export function emptyToolForm(): ToolForm {
  return { name: "", description: "", executableName: "", executablePath: "", mode: "inlineScript", artifactsJSON: "[]", interpreter: ["/usr/bin/env", "bash"], script: "", setupScript: "", verifyCommand: [] };
}

export function formFromToolSpec(spec: Record<string, unknown>, name = ""): ToolForm {
  const source = asRecord(spec.source); const executable = asRecord(spec.executable);
  let mode: ToolSourceMode = "setupScript";
  if (source.httpArtifact) mode = "httpArtifact"; else if (source.ociArtifact) mode = "ociArtifact"; else if (source.inlineScript) mode = "inlineScript";
  const inline = asRecord(source.inlineScript);
  const artifact = asRecord(source[mode]);
  return { name, description: String(spec.description ?? ""), executableName: String(executable.name ?? ""), executablePath: String(executable.path ?? ""), mode,
    artifactsJSON: json(artifact.artifacts, []), interpreter: strings(inline.interpreter), script: String(inline.script ?? ""), setupScript: String(spec.setupScript ?? ""), verifyCommand: strings(spec.verifyCommand) };
}

export function buildToolSpec(form: ToolForm): Record<string, unknown> {
  const spec: Record<string, unknown> = { executable: { name: form.executableName.trim(), path: form.executablePath.trim() }, verifyCommand: form.verifyCommand };
  if (form.description.trim()) spec.description = form.description.trim();
  if (form.mode === "setupScript") spec.setupScript = form.setupScript;
  else if (form.mode === "inlineScript") spec.source = { inlineScript: { interpreter: form.interpreter, script: form.script } };
  else spec.source = { [form.mode]: { artifacts: parseJSON<unknown[]>(form.artifactsJSON || "[]", "Artifacts") } };
  return spec;
}

export function validateToolForm(form: ToolForm, isCreate: boolean): string | null {
  const base = dnsName(form.name, isCreate); if (base) return base;
  if (!form.executableName.trim() || !form.executablePath.trim() || !form.verifyCommand.length) return "Executable name/path and verify argv are required";
  if (form.mode === "setupScript" && !form.setupScript.trim()) return "Setup script is required";
  if (form.mode === "inlineScript" && (!form.interpreter.length || !form.script.trim())) return "Interpreter argv and inline script are required";
  if (form.mode === "httpArtifact" || form.mode === "ociArtifact") { try { const items = parseJSON<unknown[]>(form.artifactsJSON, "Artifacts"); if (!items.length) return "At least one platform artifact is required"; } catch (error) { return String(error); } }
  return null;
}

export interface MCPServerForm { name: string; description: string; mode: MCPTransportMode; command: string[]; requiredEnv: string[]; endpoint: string; headersJSON: string; toolAllowlist: string[]; }
export function emptyMCPServerForm(): MCPServerForm { return { name: "", description: "", mode: "stdio", command: [], requiredEnv: [], endpoint: "", headersJSON: "[]", toolAllowlist: [] }; }
export function formFromMCPServerSpec(spec: Record<string, unknown>, name = ""): MCPServerForm {
  const transport = asRecord(spec.transport); const stdio = asRecord(transport.stdio); const http = asRecord(transport.streamableHTTP);
  const mode = transport.streamableHTTP ? "streamableHTTP" : "stdio";
  return { name, description: String(spec.description ?? ""), mode, command: strings(stdio.command), requiredEnv: strings((mode === "stdio" ? stdio : http).requiredEnv), endpoint: String(http.endpoint ?? ""), headersJSON: json(http.headers, []), toolAllowlist: strings(spec.toolAllowlist) };
}
export function buildMCPServerSpec(form: MCPServerForm): Record<string, unknown> {
  const spec: Record<string, unknown> = {}; if (form.description.trim()) spec.description = form.description.trim(); if (form.toolAllowlist.length) spec.toolAllowlist = form.toolAllowlist;
  if (form.mode === "stdio") spec.transport = { stdio: { command: form.command, ...(form.requiredEnv.length ? { requiredEnv: form.requiredEnv } : {}) } };
  else { const headers = parseJSON<unknown[]>(form.headersJSON || "[]", "Headers"); spec.transport = { streamableHTTP: { endpoint: form.endpoint.trim(), ...(headers.length ? { headers } : {}), ...(form.requiredEnv.length ? { requiredEnv: form.requiredEnv } : {}) } }; }
  return spec;
}
export function validateMCPServerForm(form: MCPServerForm, isCreate: boolean): string | null { const base = dnsName(form.name, isCreate); if (base) return base; if (form.mode === "stdio" && !form.command.length) return "stdio command argv is required"; if (form.mode === "streamableHTTP" && !form.endpoint.startsWith("https://")) return "Streamable HTTP endpoint must use HTTPS"; if (form.mode === "streamableHTTP") { try { parseJSON(form.headersJSON || "[]", "Headers"); } catch (error) { return String(error); } } return null; }

export interface MCPSetForm { name: string; description: string; serverRefs: string[]; legacy: Record<string, unknown>; }
export function emptyMCPSetForm(): MCPSetForm { return { name: "", description: "", serverRefs: [], legacy: {} }; }
export function formFromMCPSetSpec(spec: Record<string, unknown>, name = ""): MCPSetForm { return { name, description: String(spec.description ?? ""), serverRefs: Array.isArray(spec.serverRefs) ? spec.serverRefs.map((item) => String(asRecord(item).name ?? "")).filter(Boolean) : [], legacy: { ...spec } }; }
export function buildMCPSetSpec(form: MCPSetForm): Record<string, unknown> { const spec = { ...form.legacy }; const prior = Array.isArray(form.legacy.serverRefs) ? form.legacy.serverRefs.map(asRecord) : []; delete spec.description; delete spec.serverRefs; if (form.description.trim()) spec.description = form.description.trim(); if (form.serverRefs.length) spec.serverRefs = form.serverRefs.map((name) => ({ ...(prior.find((ref) => String(ref.name ?? "") === name) ?? {}), name })); return spec; }
export function validateMCPSetForm(form: MCPSetForm, isCreate: boolean): string | null { return dnsName(form.name, isCreate); }
