import { app, BrowserWindow } from "electron";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dirname = path.dirname(fileURLToPath(import.meta.url));
const productTitle = "Anvil Agents Desktop";
const host = process.env.ANVIL_DESKTOP_URL || "http://127.0.0.1:1738";

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

app.whenReady().then(() => {
  createMainWindow();
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
