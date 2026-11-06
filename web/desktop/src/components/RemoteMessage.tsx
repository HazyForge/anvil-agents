import { memo } from 'react';
import type { ChatMessage } from '../api/types.chat';
import { MarkdownBody } from './MarkdownBody';
import { ChatAssistantBody } from './ChatAssistantBody';
import { mayShowCreateAffordance } from '../wrapper/jevManagerCreate';
import { mayShowHandoffAffordance, peerHandoffTargetFromMessage, suggestPeerHandoffTarget } from '../wrapper/jevPeerHandoff';

// Read-only Jev intent caption for a classified user message. The API turn
// path records jevIntent/jevRawChoice/jevConfidence/jevModel/jevUnclear on
// the queued user message metadata (see docs/jev-intent-routing.md).
// Display only: no fulfillment action lives here — create_agent_request
// still routes to requesting a Wrapper/manager through the existing
// manager-authorization path. Returns undefined when unclassified.
function jevIntentCaption(message: {metadata?: unknown}): string | undefined {
  const meta = message.metadata as Record<string, unknown> | undefined;
  const intent = typeof meta?.jevIntent === 'string' ? meta.jevIntent.trim() : '';
  if (!intent) return undefined;
  const parts = [`Intent: ${intent}`];
  const raw = typeof meta?.jevRawChoice === 'string' ? (meta.jevRawChoice as string).trim() : '';
  const unclear = meta?.jevUnclear === true;
  if (unclear && raw && raw !== intent) parts.push(`(model said ${raw})`);
  const confidence = typeof meta?.jevConfidence === 'number' ? (meta.jevConfidence as number) : undefined;
  if (typeof confidence === 'number' && Number.isFinite(confidence)) {
    parts.push(`${Math.round(confidence * 100)}%`);
  } else if (unclear) {
    parts.push('needs clarification');
  }
  const model = typeof meta?.jevModel === 'string' ? (meta.jevModel as string).trim() : '';
  if (model) parts.push(model);
  return parts.join(' · ');
}

type Props = {
  message: ChatMessage;
  agentLabel: string;
  createPrincipal: string;
  profileNames: string[];
  /** Arrived after the conversation opened; animates in once. */
  entering: boolean;
  onCreateRequest: (content: string) => void;
  onHandoff: (content: string) => void;
  onViewOutput: (runName: string, backend: string) => void;
};

/**
 * One durable message. Memoized on stable props so a thread refresh that
 * changes nothing in this message never re-parses its Markdown.
 */
export const RemoteMessage = memo(function RemoteMessage({message, agentLabel, createPrincipal, profileNames, entering, onCreateRequest, onHandoff, onViewOutput}: Props) {
  const enter = entering ? ' chat-bubble-enter' : '';
  if (message.role === 'system') {
    const metadata = message.metadata as {kind?: string; backend?: string; runName?: string} | undefined;
    if (metadata?.kind !== 'legacy_output_unavailable' || !['hermesAgent', 'openClaw'].includes(metadata.backend || '')) return null;
    return <aside className="remote-chat-caption" aria-label="Earlier reply unavailable"><strong>Earlier reply unavailable</strong><p>{metadata.backend === 'openClaw' ? 'This older answer could not be recovered from its runner output yet. Your message is saved. The answer will be saved permanently when recovery succeeds.' : 'An older Hermes runner did not separate its final answer from internal output. The original message has been preserved. Reasoning and original output can be inspected in the runner output while its logs are retained.'}</p>{metadata.runName && /^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(metadata.runName) && <button type="button" className="btn btn-ghost" onClick={() => onViewOutput(metadata.runName!, metadata.backend!)}>View original runner output</button>}</aside>;
  }
  const jevCaption = jevIntentCaption(message);
  // Manager-only affordance: a classified, non-unclear
  // create_agent_request user message carries
  // jevNeedsManagerCreate=true. Wrapper/manager principals get a
  // "Requested create" receipt that prefills the existing
  // CreateAgentPanel; peers keep the caption only (never a
  // create button). Authority still lives in
  // isCreateAgentPrincipal + executeCreateAgent.
  const showCreateAffordance = message.role === 'user' && mayShowCreateAffordance(message, createPrincipal);
  // Handoff affordance: a classified, non-unclear peer_handoff
  // user message carries jevNeedsPeerHandoff=true, or any
  // message carries an equivalent existing-peer requestPeer
  // STATUS_JSON hint. Wrapper/manager principals get a handoff
  // receipt that opens the named peer's standing conversation
  // (existing coordination only); peers keep the caption only
  // (never a handoff button that could create). Handoff never
  // creates a teammate.
  const showHandoffAffordance = (message.role === 'user' || message.role === 'assistant') && mayShowHandoffAffordance(message, createPrincipal);
  const handoffTarget = showHandoffAffordance
    ? (peerHandoffTargetFromMessage(message)?.peerProfileName?.trim() || suggestPeerHandoffTarget(message.content, profileNames))
    : null;
  return <article className={`chat-bubble ${message.role === 'user' ? 'chat-bubble-user' : 'chat-bubble-run'}${enter}`}>
    <header className="chat-bubble-header"><span className="chat-bubble-role">{message.role === 'user' ? ((message.metadata as {authorProfile?: string} | undefined)?.authorProfile || 'You') : message.role === 'tool' ? 'Coordination' : agentLabel}</span></header>
    {message.role === 'assistant' ? <ChatAssistantBody content={message.content}/> : <div className="chat-bubble-body"><MarkdownBody content={message.content}/></div>}
    {jevCaption && <p className="remote-chat-caption">{jevCaption}</p>}
    {showCreateAffordance && (
      <div className="remote-chat-caption" role="note" aria-label="Requested agent create">
        <span>Requested create — needs a manager to fulfill.</span>{' '}
        <button type="button" className="btn btn-ghost" onClick={() => onCreateRequest(message.content)}>Review in create form</button>
      </div>
    )}
    {showHandoffAffordance && (
      <div className="remote-chat-caption" role="note" aria-label="Requested peer handoff">
        <span>Handoff requested — coordinate with an existing peer{handoffTarget ? ` (${handoffTarget})` : ''}. Never creates a teammate.</span>{' '}
        <button type="button" className="btn btn-ghost" onClick={() => onHandoff(message.content)}>{handoffTarget ? `Open ${handoffTarget} conversation` : 'Review coordination'}</button>
      </div>
    )}
  </article>;
});
