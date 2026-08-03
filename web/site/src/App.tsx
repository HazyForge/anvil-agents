import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import SiteFooter from "./components/SiteFooter";
import SiteHeader from "./components/SiteHeader";
import DocPage from "./pages/DocPage";
import DocsIndexPage from "./pages/DocsIndexPage";
import HomePage from "./pages/HomePage";

export default function App() {
  return (
    <BrowserRouter>
      <div className="shell">
        <SiteHeader />
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/docs" element={<DocsIndexPage />} />
          <Route path="/docs/*" element={<DocPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
        <SiteFooter />
      </div>
    </BrowserRouter>
  );
}
