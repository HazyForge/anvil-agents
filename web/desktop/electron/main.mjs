import { app, BrowserWindow, ipcMain } from "electron";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dirname = path.dirname(fileURLToPath(import.meta.url));
const host = process.env.ANVIL_DESKTOP_URL || "http://127.0.0.1:1738";

function createMainWindow() {
  const window = new BrowserWindow({
    width: 1280,
    height: 840,
    backgroundColor: "#0a0f0d",
    title: "Anvil Agents Desktop",
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

function createConsoleWindow(url) {
  const window = new BrowserWindow({
    width: 1280,
    height: 840,
    backgroundColor: "#0a0f0d",
    title: "Anvil Agents Console",
    autoHideMenuBar: true,
    webPreferences: {
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  void window.loadURL(url);
}

app.whenReady().then(() => {
  ipcMain.handle("open-console", (_event, url) => {
    if (typeof url !== "string" || !/^https?:\/\//.test(url)) {
      throw new Error("console URL must be http(s)");
    }
    createConsoleWindow(url);
  });
  createMainWindow();
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
