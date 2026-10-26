import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
// Bundled Forge Deck faces: the host CSP is font-src 'self'.
import "@fontsource/barlow/latin-500.css";
import "@fontsource/barlow/latin-600.css";
import "@fontsource/barlow/latin-700.css";
import "@fontsource/michroma/latin-400.css";
import "@fontsource-variable/manrope";
import "@fontsource-variable/jetbrains-mono";
import "./styles/forge-deck.css";
import "./styles/desktop.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("desktop root element missing");
}

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
