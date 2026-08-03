export type DocSection = "start" | "core" | "operate" | "integrate" | "blog";

export type DocEntry = {
  slug: string;
  /** Path relative to the repo docs/ directory */
  file: string;
  title: string;
  summary: string;
  section: DocSection;
  /** Optional hero image under /docs/ (public/docs or docs/images) */
  image?: string;
};

export const DOC_SECTIONS: { id: DocSection; label: string }[] = [
  { id: "start", label: "Start here" },
  { id: "core", label: "Core concepts" },
  { id: "operate", label: "Operate" },
  { id: "integrate", label: "Integrate" },
  { id: "blog", label: "Blog" },
];

/**
 * Operator-facing docs published on anvil-agents.hazyforge.io/docs.
 * Planning-only notes (agent-frontend-plan, design-roadmap) stay in git only.
 */
export const DOCS: DocEntry[] = [
  {
    slug: "getting-started",
    file: "getting-started.md",
    title: "Getting started",
    summary:
      "Credential-free Kind path, prerequisites, and the first successful AgentRun.",
    section: "start",
    image: "/docs/agentrun-lifecycle.jpg",
  },
  {
    slug: "architecture",
    file: "architecture.md",
    title: "Architecture",
    summary:
      "How the controller resolves runs, materializes payloads, and owns Jobs.",
    section: "core",
    image: "/docs/architecture-overview.jpg",
  },
  {
    slug: "composition",
    file: "composition.md",
    title: "Composition",
    summary:
      "Profiles, harnesses, skill sets, and tool sets as independent lifecycles.",
    section: "core",
    image: "/docs/composition-model.jpg",
  },
  {
    slug: "agent-run",
    file: "agent-run.md",
    title: "Agent runtime",
    summary: "Append-only AgentRun semantics and resource ownership.",
    section: "core",
    image: "/docs/agentrun-lifecycle.jpg",
  },
  {
    slug: "harnesses",
    file: "harnesses.md",
    title: "Harnesses",
    summary:
      "Codex, OpenCode, Hermes, OpenClaw, Grok Build, Pi, and custom runners.",
    section: "core",
    image: "/docs/multi-harness.jpg",
  },
  {
    slug: "creating-a-workable-agent",
    file: "creating-a-workable-agent.md",
    title: "Creating a workable agent",
    summary: "Practical profile and harness construction for real workloads.",
    section: "start",
  },
  {
    slug: "cli",
    file: "cli.md",
    title: "CLI",
    summary: "anvil-agentctl for runs, auth sessions, and diagnosis.",
    section: "operate",
  },
  {
    slug: "operations",
    file: "operations.md",
    title: "Operations",
    summary: "Day-2 install, upgrades, and operational boundaries.",
    section: "operate",
  },
  {
    slug: "observability",
    file: "observability.md",
    title: "Observability",
    summary: "Labels, logs, and collector-neutral telemetry hooks.",
    section: "operate",
  },
  {
    slug: "live-agent-run-stream",
    file: "live-agent-run-stream.md",
    title: "Live run stream",
    summary: "OIDC API, SSE streams, and the Anvil Agents Console.",
    section: "operate",
  },
  {
    slug: "archive",
    file: "archive.md",
    title: "Archive",
    summary: "Terminal retention and PostgreSQL archive behavior.",
    section: "operate",
  },
  {
    slug: "security",
    file: "security.md",
    title: "Security",
    summary: "RBAC, secrets, GitOps locks, and API trust boundaries.",
    section: "operate",
  },
  {
    slug: "security-and-release",
    file: "security-and-release.md",
    title: "Security and release",
    summary: "Release gates and security program for published artifacts.",
    section: "operate",
  },
  {
    slug: "volume-copy",
    file: "volume-copy.md",
    title: "Volume copy",
    summary: "AgentDataVolume copy workflows for durable agent homes.",
    section: "operate",
  },
  {
    slug: "distributed-workloads",
    file: "distributed-workloads.md",
    title: "Distributed workloads",
    summary: "Spreading heavy agent Jobs across cluster capacity.",
    section: "integrate",
  },
  {
    slug: "integrating-adverse-sources",
    file: "integrating-adverse-sources.md",
    title: "Adverse sources",
    summary: "AdverseSituation and AdverseSignal integrations.",
    section: "integrate",
  },
  {
    slug: "integrating-knowledge-and-tools",
    file: "integrating-knowledge-and-tools.md",
    title: "Knowledge and tools",
    summary: "Skills, tools, and external service contracts.",
    section: "integrate",
  },
  {
    slug: "migration-from-anvil-primaris",
    file: "migration-from-anvil-primaris.md",
    title: "Migration from Anvil Primaris",
    summary: "Moving off the in-tree agent runtime to standalone anvil-agents.",
    section: "integrate",
  },
  {
    slug: "release-primaris",
    file: "release-primaris.md",
    title: "Release on Primaris",
    summary: "Hazy Forge consumer release notes for anvil-primaris overlays.",
    section: "integrate",
  },
  {
    slug: "blog/agents-as-cluster-jobs-not-chatbots",
    file: "blog/2026-08-02-agents-as-cluster-jobs-not-chatbots.md",
    title: "Agents as cluster jobs, not chatbots",
    summary:
      "Why durable agent work belongs on Kubernetes Jobs with composable objects.",
    section: "blog",
    image: "/docs/composition-model.jpg",
  },
];

export function docBySlug(slug: string): DocEntry | undefined {
  return DOCS.find((d) => d.slug === slug);
}

export function docsInSection(section: DocSection): DocEntry[] {
  return DOCS.filter((d) => d.section === section);
}
