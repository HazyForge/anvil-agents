import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { cardSummary, type AgentCard } from "../../cards/types";
import { deleteCard, exportCardsJSON, importCardsJSON, listCards } from "../../cards/store";
import { formatTime } from "../../utils/format";

interface Props {
  namespace: string;
  runsCreateEnabled: boolean;
}

export function CardsPage({ namespace, runsCreateEnabled }: Props) {
  const navigate = useNavigate();
  const [cards, setCards] = useState<AgentCard[]>([]);
  const [query, setQuery] = useState("");
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    setCards(listCards(namespace));
  }, [namespace]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) {
      return cards;
    }
    return cards.filter((card) => {
      const hay = [
        card.title,
        card.description,
        card.profileName,
        card.harnessProfileName,
        ...card.skillSetNames,
        ...card.toolSetNames,
        ...card.tags,
        card.prompt,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return hay.includes(q);
    });
  }, [cards, query]);

  function refresh() {
    setCards(listCards(namespace));
  }

  function onDelete(id: string, title: string) {
    if (!window.confirm(`Delete card “${title}”? This only removes the browser recipe.`)) {
      return;
    }
    deleteCard(id);
    refresh();
  }

  function onExport() {
    const blob = new Blob([exportCardsJSON(namespace)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `anvil-cards-${namespace || "all"}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  function onImport(file: File | null) {
    if (!file) {
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const count = importCardsJSON(String(reader.result ?? ""), namespace);
        setMessage(`Imported ${count} card(s)`);
        refresh();
      } catch (err) {
        setMessage(err instanceof Error ? err.message : String(err));
      }
    };
    reader.readAsText(file);
  }

  if (!namespace) {
    return (
      <div className="panel">
        <div className="panel-body empty">Select a namespace to manage agent cards.</div>
      </div>
    );
  }

  return (
    <div className="cards-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Agent cards</h1>
          <p className="page-sub">
            Browser recipes for namespace <span className="mono">{namespace}</span>. Cards assemble
            profile / harness / skills / tools + prompt, then launch an append-only AgentRun.
          </p>
        </div>
        <div className="chip-row">
          <button type="button" className="btn" onClick={onExport}>
            Export
          </button>
          <label className="btn">
            Import
            <input
              type="file"
              accept="application/json,.json"
              style={{ display: "none" }}
              onChange={(event) => onImport(event.target.files?.[0] ?? null)}
            />
          </label>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => navigate("/cards/new")}
          >
            New card
          </button>
        </div>
      </div>

      <div className="banner banner-info">
        Cards live in this browser only (localStorage). They are not Kubernetes objects. Running a
        card creates a new append-only AgentRun via the API
        {runsCreateEnabled ? "." : " — run create is currently disabled on this API."}
      </div>

      {message ? <div className="banner banner-ok">{message}</div> : null}

      <div className="filters-bar">
        <label className="field" style={{ flex: 1, minWidth: "14rem" }}>
          <span className="label">Search cards</span>
          <input
            className="input"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="title, profile, skill, prompt…"
          />
        </label>
      </div>

      {filtered.length === 0 ? (
        <div className="panel">
          <div className="panel-body empty">
            No cards yet. Create one to assemble a run recipe for this namespace.
          </div>
        </div>
      ) : (
        <div className="card-grid">
          {filtered.map((card) => (
            <article key={card.id} className="agent-card">
              <header className="agent-card-header">
                <h2 className="agent-card-title">{card.title}</h2>
                {card.tags.length > 0 ? (
                  <div className="chip-row">
                    {card.tags.map((tag) => (
                      <span key={tag} className="chip">
                        {tag}
                      </span>
                    ))}
                  </div>
                ) : null}
              </header>
              <p className="agent-card-desc">{card.description || "No description."}</p>
              <div className="agent-card-meta mono">{cardSummary(card)}</div>
              {card.intent ? <div className="agent-card-meta">intent · {card.intent}</div> : null}
              {card.lastRunName ? (
                <div className="agent-card-meta">
                  last run{" "}
                  <Link
                    to={`/ns/${encodeURIComponent(card.namespace)}/runs/${encodeURIComponent(card.lastRunName)}`}
                  >
                    {card.lastRunName}
                  </Link>{" "}
                  · {formatTime(card.lastRunAt)}
                </div>
              ) : null}
              <footer className="agent-card-actions">
                <button
                  type="button"
                  className="btn btn-primary"
                  onClick={() => navigate(`/cards/${encodeURIComponent(card.id)}`)}
                >
                  Open
                </button>
                <button
                  type="button"
                  className="btn btn-danger"
                  onClick={() => onDelete(card.id, card.title)}
                >
                  Delete
                </button>
              </footer>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}
