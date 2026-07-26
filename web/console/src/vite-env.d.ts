/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Optional absolute API origin for local tooling. Prefer relative /api in production. */
  readonly VITE_API_BASE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
