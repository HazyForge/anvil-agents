/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_DESKTOP_HOST?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

interface AnvilDesktopBridge {
  openConsole?: (url: string) => Promise<void> | void;
}

export {};

declare global {
  interface Window {
    anvilDesktop?: AnvilDesktopBridge;
  }
}
