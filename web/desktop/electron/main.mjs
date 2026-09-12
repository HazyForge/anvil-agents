import { app, BrowserWindow } from "electron";
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dirname = path.dirname(fileURLToPath(import.meta.url));
const productTitle = "Anvil Agents Desktop";
const host = process.env.ANVIL_DESKTOP_URL || "http://127.0.0.1:1738";
const listen = process.env.ANVIL_DESKTOP_LISTEN || "127.0.0.1:1738";

let sidecar = null;

function sidecarPath() {
  const name = process.platform === "win32" ? "anvil-desktop.exe" : "anvil-desktop";
  const packaged = path.join(process.resourcesPath || "", "sidecar", name);
  if (existsSync(packaged)) {
    return packaged;
  }
  const nearby = path.join(dirname, "..", "..", "..", "dist", "desktop", "bin", name);
  if (existsSync(nearby)) {
    return nearby;
  }
  return "";
}

async function waitForHealth(url, attempts = 50) {
  for (let i = 0; i < attempts; i++) {
    try {
      const response = await fetch(`${url}/healthz`, { cache: "no-store" });
      if (response.ok) {
        return true;
      }
    } catch {
      /* retry */
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  return false;
}

async function ensureSidecar() {
  if (await waitForHealth(host, 2)) {
    return;
  }
  const bin = sidecarPath();
  if (!bin) {
    return;
  }
  const args = ["--listen", listen];
  const origin = process.env.ANVIL_DESKTOP_API_ORIGIN;
  if (origin) {
    args.push("--api-origin", origin);
  }
  const target = process.env.ANVIL_DESKTOP_HARNESS_TARGET;
  if (target) {
    args.push("--harness-target", target);
  }
  sidecar = spawn(bin, args, {
    stdio: "ignore",
    windowsHide: true,
  });
  sidecar.unref();
  await waitForHealth(host);
}

function createMainWindow() {
  const window = new BrowserWindow({
    width: 1280,
    height: 840,
    backgroundColor: "#0a0f0d",
    title: productTitle,
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(dirname, "preload.cjs"),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  void window.loadURL(host);
}

app.setName(productTitle);

app.whenReady().then(async () => {
  await ensureSidecar();
  createMainWindow();
});

app.on("window-all-closed", () => {
  if (sidecar && !sidecar.killed) {
    sidecar.kill();
  }
  if (process.platform !== "darwin") {
    app.quit();
  }
});
