import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
// Forge Deck type roles, bundled locally: the API CSP is font-src 'self'.
import "@fontsource/barlow/latin-500.css";
import "@fontsource/barlow/latin-600.css";
import "@fontsource/barlow/latin-700.css";
import "@fontsource/michroma/latin-400.css";
import "@fontsource-variable/manrope";
import "@fontsource-variable/jetbrains-mono";
import "./styles/forge-deck.css";
import "./styles/shell.css";
import "./styles/runs.css";
import "./styles/chat.css";
import "./styles/composition.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("console root element missing");
}

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
