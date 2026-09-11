const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("anvilDesktop", {
  openConsole(url) {
    return ipcRenderer.invoke("open-console", url);
  },
});
