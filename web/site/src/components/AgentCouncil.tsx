import { useEffect, useMemo, useState } from "react";

type Seat = {
  id: string;
  label: string;
  role: string;
  angle: number;
};

const SEATS: Seat[] = [
  { id: "codex", label: "Codex", role: "LLM harness", angle: -90 },
  { id: "opencode", label: "OpenCode", role: "Code exec", angle: -30 },
  { id: "hermes", label: "Hermes", role: "Routing", angle: 30 },
  { id: "openclaw", label: "OpenClaw", role: "Tooling", angle: 90 },
  { id: "grok", label: "Grok Build", role: "Build agent", angle: 150 },
  { id: "pi", label: "Pi", role: "Reasoning", angle: 210 },
];

const JOBS = [
  { name: "job/anvil-codex-7f3k9", phase: "Running", age: "14m", harness: "Codex" },
  { name: "job/opencode-runner-4p2m", phase: "Running", age: "6m", harness: "OpenCode" },
  { name: "job/hermes-scan-9c1a", phase: "Succeeded", age: "41m", harness: "Hermes" },
];

const SIZE = 420;
const CX = SIZE / 2;
const CY = SIZE / 2;
const RING_R = 128;
const SEAT_R = 30;
const CORE_R = 48;

function polar(angleDeg: number, radius: number) {
  const rad = (angleDeg * Math.PI) / 180;
  return {
    x: CX + Math.cos(rad) * radius,
    y: CY + Math.sin(rad) * radius,
  };
}

export default function AgentCouncil() {
  const [tick, setTick] = useState(0);
  const [reduceMotion, setReduceMotion] = useState(false);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    setReduceMotion(mq.matches);
    const onChange = () => setReduceMotion(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  useEffect(() => {
    if (reduceMotion) return;
    const id = window.setInterval(() => setTick((t) => t + 1), 80);
    return () => window.clearInterval(id);
  }, [reduceMotion]);

  const pulse = useMemo(() => (reduceMotion ? 0 : (tick % 120) / 120), [tick, reduceMotion]);
  const orbit = useMemo(() => (reduceMotion ? 0 : (tick * 0.35) % 360), [tick, reduceMotion]);

  return (
    <div className="council" role="img" aria-label="Agent council: multi-harness seats orbiting the Anvil controller, dispatching Kubernetes Jobs">
      <div className="council-chrome">
        <div className="council-chrome-top">
          <span className="mono council-kicker">Agent Council // Active</span>
          <span className="chip">
            <span className="chip-dot" />
            Live
          </span>
        </div>

        <div className="council-stage">
          <svg viewBox={`0 0 ${SIZE} ${SIZE}`} className="council-svg" aria-hidden="true">
            <defs>
              <linearGradient id="ring" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="rgb(47 217 176)" stopOpacity="0.55" />
                <stop offset="55%" stopColor="rgb(61 255 154)" stopOpacity="0.9" />
                <stop offset="100%" stopColor="rgb(232 255 244)" stopOpacity="0.28" />
              </linearGradient>
              <radialGradient id="core" cx="50%" cy="40%" r="60%">
                <stop offset="0%" stopColor="rgb(61 255 154)" stopOpacity="0.5" />
                <stop offset="45%" stopColor="rgb(47 217 176)" stopOpacity="0.22" />
                <stop offset="100%" stopColor="rgb(3 8 6)" stopOpacity="0.95" />
              </radialGradient>
              <filter id="glow" x="-40%" y="-40%" width="180%" height="180%">
                <feGaussianBlur stdDeviation="3.2" result="b" />
                <feMerge>
                  <feMergeNode in="b" />
                  <feMergeNode in="SourceGraphic" />
                </feMerge>
              </filter>
            </defs>

            <circle
              cx={CX}
              cy={CY}
              r={RING_R + 40}
              stroke="rgb(232 255 244 / 0.08)"
              strokeWidth="1"
              strokeDasharray="2 9"
              fill="none"
            />
            <circle
              cx={CX}
              cy={CY}
              r={RING_R + 20}
              stroke="url(#ring)"
              strokeWidth="1"
              strokeOpacity="0.5"
              fill="none"
            />
            <circle
              cx={CX}
              cy={CY}
              r={RING_R}
              stroke="rgb(61 255 154 / 0.28)"
              strokeWidth="1.25"
              strokeDasharray="6 10"
              fill="none"
              transform={`rotate(${orbit} ${CX} ${CY})`}
            />

            {SEATS.map((seat, index) => {
              const outer = polar(seat.angle, RING_R - SEAT_R - 2);
              const inner = polar(seat.angle, CORE_R + 10);
              const beadT = (pulse + index * 0.14) % 1;
              const bx = outer.x + (inner.x - outer.x) * beadT;
              const by = outer.y + (inner.y - outer.y) * beadT;
              return (
                <g key={`beam-${seat.id}`}>
                  <line
                    x1={inner.x}
                    y1={inner.y}
                    x2={outer.x}
                    y2={outer.y}
                    stroke="rgb(61 255 154 / 0.18)"
                    strokeWidth="1"
                  />
                  {!reduceMotion && (
                    <circle
                      cx={bx}
                      cy={by}
                      r="2.6"
                      fill="rgb(61 255 154)"
                      filter="url(#glow)"
                      opacity={0.35 + Math.sin(beadT * Math.PI) * 0.65}
                    />
                  )}
                </g>
              );
            })}

            <circle
              cx={CX}
              cy={CY}
              r={CORE_R + 12}
              stroke="rgb(61 255 154 / 0.2)"
              strokeWidth="1"
              strokeDasharray="4 6"
              fill="none"
            />
            <circle
              cx={CX}
              cy={CY}
              r={CORE_R}
              fill="url(#core)"
              stroke="rgb(61 255 154 / 0.6)"
              strokeWidth="1.5"
              filter="url(#glow)"
            />
            <circle
              cx={CX}
              cy={CY}
              r={15}
              fill="rgb(3 8 6 / 0.62)"
              stroke="rgb(232 255 244 / 0.35)"
              strokeWidth="1"
            />
            <circle
              cx={CX}
              cy={CY}
              r={6.5}
              fill="rgb(61 255 154)"
              opacity={reduceMotion ? 1 : 0.55 + Math.sin(pulse * Math.PI * 2) * 0.35}
            />
            <text
              x={CX}
              y={CY + 3}
              textAnchor="middle"
              fill="rgb(232 255 244 / 0.92)"
              style={{
                fontFamily: "IBM Plex Mono, ui-monospace, monospace",
                fontSize: 11,
                fontWeight: 600,
                letterSpacing: "0.16em",
              }}
            >
              ANVIL
            </text>
            <text
              x={CX}
              y={CY + CORE_R + 18}
              textAnchor="middle"
              fill="rgb(61 255 154)"
              style={{
                fontFamily: "IBM Plex Mono, ui-monospace, monospace",
                fontSize: 9,
                letterSpacing: "0.14em",
              }}
            >
              CONTROLLER
            </text>
            <text
              x={CX}
              y={CY + CORE_R + 30}
              textAnchor="middle"
              fill="rgb(232 255 244 / 0.36)"
              style={{
                fontFamily: "IBM Plex Mono, ui-monospace, monospace",
                fontSize: 8,
                letterSpacing: "0.12em",
              }}
            >
              JOB QUEUE
            </text>

            {SEATS.map((seat) => {
              const pos = polar(seat.angle, RING_R);
              return (
                <g key={seat.id}>
                  <circle
                    cx={pos.x}
                    cy={pos.y}
                    r={SEAT_R + 5}
                    stroke="rgb(61 255 154 / 0.16)"
                    strokeWidth="1"
                    fill="none"
                  />
                  <circle
                    cx={pos.x}
                    cy={pos.y}
                    r={SEAT_R}
                    fill="rgb(3 8 6 / 0.9)"
                    stroke="rgb(61 255 154 / 0.45)"
                    strokeWidth="1.25"
                  />
                  <text
                    x={pos.x}
                    y={pos.y - 2}
                    textAnchor="middle"
                    fill="rgb(232 255 244 / 0.92)"
                    style={{
                      fontFamily: "Barlow, sans-serif",
                      fontSize: 10,
                      fontWeight: 700,
                      letterSpacing: "0.06em",
                    }}
                  >
                    {seat.label.toUpperCase()}
                  </text>
                  <text
                    x={pos.x}
                    y={pos.y + 11}
                    textAnchor="middle"
                    fill="rgb(232 255 244 / 0.38)"
                    style={{
                      fontFamily: "IBM Plex Mono, ui-monospace, monospace",
                      fontSize: 7,
                      letterSpacing: "0.1em",
                    }}
                  >
                    {seat.role.toUpperCase()}
                  </text>
                </g>
              );
            })}
          </svg>
        </div>

        <div className="council-jobs">
          {JOBS.map((job) => (
            <div key={job.name} className="council-job">
              <div className="council-job-name mono">{job.name}</div>
              <div className="council-job-meta">
                <span className={job.phase === "Running" ? "ok" : "done"}>{job.phase}</span>
                <span>{job.age} ego</span>
                <span>{job.harness}</span>
              </div>
            </div>
          ))}
        </div>

        <div className="council-footer mono">
          <span>Cluster load 63%</span>
          <span>6 agent cycles</span>
          <span>9m until next reconcile</span>
        </div>
      </div>

      <style>{`
        .council {
          width: 100%;
        }
        .council-chrome {
          border: 1px solid rgb(61 255 154 / 0.16);
          border-radius: 1rem;
          background:
            radial-gradient(circle at 70% 20%, rgb(61 255 154 / 0.08), transparent 42%),
            linear-gradient(180deg, rgb(232 255 244 / 0.03), rgb(3 8 6 / 0.5));
          box-shadow: 0 0 60px rgb(61 255 154 / 0.06), inset 0 1px 0 rgb(232 255 244 / 0.05);
          overflow: hidden;
        }
        .council-chrome-top {
          display: flex;
          align-items: center;
          justify-content: space-between;
          gap: 1rem;
          padding: 1rem 1.15rem 0.4rem;
        }
        .council-kicker {
          color: rgb(61 255 154 / 0.9);
          font-size: 0.68rem;
        }
        .council-stage {
          padding: 0.25rem 0.5rem 0.5rem;
        }
        .council-svg {
          width: 100%;
          height: auto;
          max-height: 420px;
          margin: 0 auto;
        }
        .council-jobs {
          display: grid;
          gap: 0.55rem;
          padding: 0 1rem 0.85rem;
        }
        .council-job {
          border: 1px solid rgb(232 255 244 / 0.08);
          border-radius: 0.55rem;
          background: rgb(3 8 6 / 0.45);
          padding: 0.55rem 0.7rem;
        }
        .council-job-name {
          color: rgb(232 255 244 / 0.78);
          font-size: 0.62rem;
          letter-spacing: 0.08em;
        }
        .council-job-meta {
          display: flex;
          flex-wrap: wrap;
          gap: 0.65rem;
          margin-top: 0.28rem;
          color: rgb(232 255 244 / 0.38);
          font-family: var(--font-mono);
          font-size: 0.62rem;
          letter-spacing: 0.08em;
          text-transform: uppercase;
        }
        .council-job-meta .ok {
          color: rgb(61 255 154 / 0.92);
        }
        .council-job-meta .done {
          color: rgb(47 217 176 / 0.85);
        }
        .council-footer {
          display: flex;
          flex-wrap: wrap;
          gap: 0.85rem 1.2rem;
          padding: 0.7rem 1rem 0.95rem;
          border-top: 1px solid rgb(232 255 244 / 0.07);
          color: rgb(232 255 244 / 0.34);
          font-size: 0.6rem;
        }
      `}</style>
    </div>
  );
}
