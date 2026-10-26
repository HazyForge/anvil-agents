import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import type { UIConfig } from '../auth/config';
import { APIError, listRunProfiles, type CompositionDocument } from '../api/client';
import { getRemoteThread, type RemoteThreadDetail } from '../api/remoteChat';
import { openChatThreadStream } from '../api/chatThreadStream';
import { listRoutingRequests, listSubjects, sendManagerMessage, updateSubject, mergeSubjects, type ManagerMessage, type RoutingRequest, type Subject } from '../api/subjectRouting';
import { loadNamespace, saveNamespace } from '../state/namespace';
import { preferredStandingChatProfile } from '../wrapper/chatLanding';
import { DesktopIcon } from '../components/AgentAvatar';
import { ChatAssistantBody } from '../components/ChatAssistantBody';
import { RemoteTurnActivity } from '../components/RemoteTurnActivity';

const intentNames: Record<string, string> = {
  question: 'Question', amend_running: 'Change existing work', new_work: 'New work',
  status: 'Status check', decide: 'Decision', unclear: 'Needs clarification',
  unclassified: 'Agent will interpret the request',
};
const decisionReasons: Record<string, string> = {
  user_selected: 'You selected the subject for this message.', matched_subject: 'Matched to an existing subject.',
  subject_uncertain: 'The subject match needs your confirmation.', subject_ambiguous: 'More than one subject could fit.',
  unclear_intent: 'The request needs clarification.', intent_uncertain: 'The intended action needs your confirmation.',
  new_subject_uncertain: 'Confirm whether this should start a new subject.', new_subject: 'This message starts a new subject.',
  general_conversation: 'Saved with general conversation.', provider_unavailable: 'Automatic classification is unavailable. You can choose a subject.',
  invalid_decision: 'No reliable decision was returned. Choose a subject to continue.', invalid_input: 'The message could not be classified.',
};
const choiceName = (id: string, subjects: Subject[]) => id === 'new_subject' ? 'New subject' : id === 'none' ? 'General conversation' : subjects.find(subject => subject.id === id)?.label || id;
const clockTime = (at?: string) => at && Number.isFinite(Date.parse(at)) ? new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '';
const percent = (value?: number) => typeof value === 'number' && Number.isFinite(value) ? `${Math.round(value * 100)}%` : '—';
const errorText = (error: unknown) => error instanceof Error ? error.message : 'The manager could not be reached.';
const eventsOf = (request?: RoutingRequest) => request?.events ?? [];
const requestThread = (request?: RoutingRequest) => request?.result?.thread?.id || request?.threadId || '';
const chronological = (requests: RoutingRequest[]) => [...requests].sort((a, b) => a.createdAt.localeCompare(b.createdAt));
const activeTurnStatus = (status?: string) => status === 'waiting' || status === 'queued' || status === 'running';

function RoutingTimeline({ request, subjects, busy }: { request?: RoutingRequest; subjects: Subject[]; busy: boolean }) {
  const events = eventsOf(request);
  const decision = [...events].reverse().find(event => event.decision)?.decision;
  const candidates = [...events].reverse().find(event => event.candidates)?.candidates ?? [];
  const choices = decision?.subjectProbabilities ? Object.entries(decision.subjectProbabilities).sort((a, b) => b[1] - a[1]) : [];
  const labelFor = (id: string) => candidates.find(candidate => candidate.id === id)?.label || choiceName(id, subjects);
  return <aside className="manager-routing" aria-label="Routing details">
    <div className="manager-panel-heading"><span className={`manager-signal${busy ? ' is-live' : ''}`} aria-hidden="true"/><h2>Routing {busy ? 'live' : 'details'}</h2></div>
    <p className="manager-panel-note">The choices and handoff reported for this message.</p>
    {!request && <div className="manager-routing-empty"><DesktopIcon kind="activity"/><p>Follow a message from the subject search to its destination.</p><span>Steps appear as they happen.</span></div>}
    {request && <>
      <ol className="manager-timeline" aria-label="Routing steps">
        {events.map((event, index) => <li key={`${index}-${event.step}`} className={`routing-step routing-step-${event.status === 'running' && (!busy || index < events.length - 1) ? 'started' : event.status}`}>
          <span className="routing-step-marker" aria-hidden="true">{event.status === 'completed' ? '✓' : event.status === 'failed' ? '!' : event.status === 'waiting' ? '?' : '·'}</span>
          <div><div className="routing-step-title"><strong>{event.label}</strong><time dateTime={event.at}>{clockTime(event.at)}</time></div>{event.detail && <p>{event.detail}</p>}</div>
        </li>)}
      </ol>
      {!events.length && <p className="manager-panel-note">Waiting for the first server update…</p>}
      {choices.length > 0 && <section className="routing-choices" aria-label="Subject probabilities">
        <h3>Subject choices</h3>
        {choices.map(([id, probability]) => <div key={id} className={`routing-choice${decision?.subjectChoice === id ? ' is-chosen' : ''}`}>
          <div><span>{labelFor(id)}</span><strong>{percent(probability)}</strong></div>
          <meter min={0} max={1} value={probability} aria-label={`${labelFor(id)} probability`}/>
        </div>)}
      </section>}
      {decision?.reason === 'user_selected' && <section className="routing-verdict"><span className="manager-eyebrow">Subject selected by you</span><div><strong>{labelFor(decision.subjectChoice)}</strong></div></section>}
      {decision && <section className="routing-verdict" aria-label="Intent choice"><span className="manager-eyebrow">Intent</span><div><strong>{intentNames[decision.intent] || decision.intent || 'Not classified'}</strong><span>{percent(decision.model ? decision.intentConfidence : undefined)}</span></div>{decision.reason && decisionReasons[decision.reason] && <p>{decisionReasons[decision.reason]}</p>}{decision.model && <small>{decision.model}</small>}</section>}
      {request.status === 'routing' && !busy && <p className="manager-interrupted">Routing has not been confirmed. Recover this message before sending it again.</p>}
    </>}
  </aside>;
}

export function ManagerPage({ token, config }: { token: string; config: UIConfig }) {
  const [namespace, setNamespace] = useState(() => loadNamespace(config.defaultNamespaces[0] || 'agents'));
  const [subjects, setSubjects] = useState<Subject[]>([]);
  const [requests, setRequests] = useState<RoutingRequest[]>([]);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);
  const [profile, setProfile] = useState('');
  const [subjectID, setSubjectID] = useState('');
  const [selectedRequestID, setSelectedRequestID] = useState('');
  const [details, setDetails] = useState<Record<string, RemoteThreadDetail>>({});
  const [liveText, setLiveText] = useState<{ turnId: string; text: string } | null>(null);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [available, setAvailable] = useState(true);
  const [error, setError] = useState('');
  const [streamError, setStreamError] = useState('');
  const [rosterError, setRosterError] = useState('');
  const [backgroundUnavailable, setBackgroundUnavailable] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editLabel, setEditLabel] = useState('');
  const [editSummary, setEditSummary] = useState('');
  const [editKind, setEditKind] = useState('project');
  const [editAliases, setEditAliases] = useState('');
  const [mergeTarget, setMergeTarget] = useState('');
  const [savingSubject, setSavingSubject] = useState(false);
  const epoch = useRef(0);
  const sending = useRef(false);
  const sendController = useRef<AbortController | null>(null);
  const transcriptEnd = useRef<HTMLDivElement>(null);
  const enabled = Boolean(config.chat?.enabled) && config.chat?.subjectRoutingEnabled !== false;
  const selectedSubject = subjects.find(subject => subject.id === subjectID);
  const selectedRequest = requests.find(request => request.id === selectedRequestID);
  const threadID = requestThread(selectedRequest) || selectedSubject?.threadId || '';
  const thread = details[threadID];
  const selectedTurn = thread?.turns?.find(turn => turn.id === (selectedRequest?.result?.turn?.id || selectedRequest?.turnId));
  const shownRequests = subjectID ? requests.filter(request => request.subjectId === subjectID || request.result?.subject?.id === subjectID) : requests;
  const unconfirmed = requests.some(request => request.status === 'routing');
  const sendingProfile = selectedSubject?.profileName || profile;
  const backgroundThreadKey = [...new Set(requests.filter(request => request.status === 'accepted').map(requestThread))]
    .filter(id => id && id !== threadID && (details[id]
      ? activeTurnStatus(details[id].activeTurn?.status) || details[id].turns?.some(turn => activeTurnStatus(turn.status))
      : requests.some(request => requestThread(request) === id && activeTurnStatus(request.result?.turn?.status))))
    .sort().join(',');

  const refresh = useCallback(async (signal?: AbortSignal, chooseLatest = true) => {
    const currentEpoch = epoch.current;
    const [vault, history] = await Promise.all([listSubjects(token, namespace, signal), listRoutingRequests(token, namespace, signal)]);
    if (signal?.aborted || currentEpoch !== epoch.current) return;
    setAvailable(vault.enabled);
    setSubjects(vault.items.filter(subject => subject.status !== 'merged'));
    setRequests(previous => {
      // Do not replace a live event stream with an older list snapshot.
      const live = previous.filter(request => sending.current && request.status === 'routing');
      return chronological([...history.filter(request => !live.some(item => item.id === request.id)), ...live]);
    });
    if (chooseLatest) setSelectedRequestID(previous => previous || chronological(history).at(-1)?.id || '');
    const ids = [...new Set(history.map(requestThread).filter(Boolean))];
    await Promise.allSettled(ids.map(async id => {
      const detail = await getRemoteThread(token, namespace, id, signal);
      if (!signal?.aborted && currentEpoch === epoch.current) setDetails(previous => ({ ...previous, [id]: detail }));
    }));
  }, [token, namespace]);

  useEffect(() => {
    epoch.current++;
    sendController.current?.abort(); sending.current = false;
    setBusy(false); setRequests([]); setSubjects([]); setDetails({}); setSelectedRequestID(''); setSubjectID('');
    setProfile(''); setDraft(''); setError(''); setStreamError(''); setRosterError(''); setBackgroundUnavailable(false); setEditing(false); setAvailable(true);
    const controller = new AbortController();
    if (!enabled) { setLoading(false); return () => controller.abort(); }
    setLoading(true);
    const roster = listRunProfiles(token, namespace, controller.signal).then(items => {
      if (controller.signal.aborted) return;
      setProfiles(items);
      setProfile(preferredStandingChatProfile(items)?.metadata.name || items[0]?.metadata.name || '');
    }).catch(() => {
      if (!controller.signal.aborted) { setProfiles([]); setRosterError('Could not load agents for new subjects. Your existing subjects and history remain available.'); }
    });
    void Promise.all([refresh(controller.signal), roster]).catch(cause => {
      if (controller.signal.aborted) return;
      if (cause instanceof APIError && cause.status === 404) setAvailable(false);
      else setError(errorText(cause));
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => { controller.abort(); sendController.current?.abort(); epoch.current++; };
  }, [refresh, enabled, token, namespace]);

  // The existing thread stream supplies snapshots and, on standing WebSocket
  // connections, live tokens. GET refresh also covers Job execution and the
  // snapshot-only SSE fallback. Neither path invents an assistant answer.
  useEffect(() => {
    if (!threadID || !enabled) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let handle: { abort: () => void } | undefined;
    let lastTokenSeq = 0;
    let tokenTurn = '';
    setLiveText(null); setStreamError('');
    const read = async () => {
      try {
        const detail = await getRemoteThread(token, namespace, threadID, controller.signal);
        if (controller.signal.aborted) return;
        setDetails(previous => ({ ...previous, [threadID]: detail }));
        setStreamError('');
        const active = detail.activeTurn || detail.turns?.find(turn => ['waiting', 'queued', 'running'].includes(turn.status));
        if (active) timer = setTimeout(() => void read(), 2000);
        else setLiveText(null);
      } catch (cause) {
        if (!controller.signal.aborted) { setStreamError(errorText(cause)); timer = setTimeout(() => void read(), 5000); }
      }
    };
    handle = openChatThreadStream(token, namespace, threadID, {
      onEvent: (event, raw) => {
        if (controller.signal.aborted) return;
        const data = raw as unknown as { thread?: RemoteThreadDetail; messages?: RemoteThreadDetail['messages']; turns?: RemoteThreadDetail['turns']; activeTurn?: RemoteThreadDetail['activeTurn']; threadId?: string; turnId?: string; seq?: number; token?: string; done?: boolean };
        if (event === 'snapshot' && data.thread) setDetails(previous => ({ ...previous, [threadID]: { ...data.thread!, messages: data.messages ?? [], turns: data.turns ?? [], activeTurn: data.activeTurn } }));
        if (event === 'token' && data.threadId === threadID && data.turnId) {
          if (data.turnId !== tokenTurn) { tokenTurn = data.turnId; lastTokenSeq = 0; setLiveText({ turnId: tokenTurn, text: '' }); }
          if ((data.seq ?? 0) > lastTokenSeq) {
            lastTokenSeq = data.seq!;
            setLiveText(previous => ({ turnId: tokenTurn, text: (previous?.turnId === tokenTurn ? previous.text : '') + (data.token || '') }));
          }
          if (data.done) { clearTimeout(timer); void read(); }
        }
      },
      onTransportError: () => { if (!controller.signal.aborted) setStreamError('Live connection interrupted. Checking the saved conversation.'); },
    }, { signal: controller.signal });
    void read();
    return () => { controller.abort(); clearTimeout(timer); handle?.abort(); };
  }, [token, namespace, threadID, selectedRequest?.result?.turn?.id, enabled]);

  // Keep other active subjects current in the central transcript. A single
  // cancellable loop reads at most four threads concurrently and drops terminal
  // threads from the next cycle; only the selected thread owns a live stream.
  useEffect(() => {
    setBackgroundUnavailable(false);
    if (!enabled || !backgroundThreadKey) return;
    const ids = backgroundThreadKey.split(',');
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      let index = 0;
      let unavailable = false;
      const worker = async () => {
        while (!controller.signal.aborted && index < ids.length) {
          const id = ids[index++];
          try {
            const detail = await getRemoteThread(token, namespace, id, controller.signal);
            if (!controller.signal.aborted) setDetails(previous => ({ ...previous, [id]: detail }));
          } catch {
            if (!controller.signal.aborted) unavailable = true;
          }
        }
      };
      await Promise.all(Array.from({ length: Math.min(4, ids.length) }, worker));
      if (!controller.signal.aborted) {
        setBackgroundUnavailable(unavailable);
        timer = setTimeout(() => void poll(), 3000);
      }
    };
    void poll();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [token, namespace, backgroundThreadKey, enabled]);

  useEffect(() => { transcriptEnd.current?.scrollIntoView({ block: 'nearest' }); }, [requests.length, subjectID, liveText?.text]);

  async function send(message: ManagerMessage) {
    if (sending.current) return;
    if (new TextEncoder().encode(message.content).byteLength > 65536) {
      setError('This message is larger than 64 KiB. Shorten it before sending.');
      return;
    }
    sending.current = true; setBusy(true); setError('');
    const currentEpoch = epoch.current;
    const controller = new AbortController(); sendController.current = controller;
    setSelectedRequestID(message.requestId);
    setRequests(previous => {
      const existing = previous.find(request => request.id === message.requestId);
      return chronological([...previous.filter(request => request.id !== message.requestId), {
        ...existing, id: message.requestId, content: message.content, profileName: message.profileName,
        subjectId: message.subjectId || existing?.subjectId,
        status: 'routing', admissionRejected: false, events: existing?.events ?? [], createdAt: existing?.createdAt || new Date().toISOString(),
      }]);
    });
    try {
      const result = await sendManagerMessage(token, namespace, message, event => {
        if (currentEpoch !== epoch.current || controller.signal.aborted) return;
        setRequests(previous => previous.map(request => request.id === message.requestId ? {
          ...request, events: [...request.events, event],
        } : request));
      }, controller.signal);
      if (currentEpoch !== epoch.current || controller.signal.aborted) return;
      setRequests(previous => previous.map(request => request.id === message.requestId ? {
        ...request, status: result.status === 'failed' && result.turn ? 'routing' : result.status, result, events: result.events ?? request.events,
        subjectId: result.subject?.id || request.subjectId, threadId: result.thread?.id || request.threadId, turnId: result.turn?.id || request.turnId,
      } : request));
      if (result.subject) setSubjects(previous => [result.subject!, ...previous.filter(subject => subject.id !== result.subject!.id)]);
      if (result.status === 'accepted' || result.status === 'clarify') setDraft('');
      if (result.status === 'accepted') {
        setDraft('');
        if (result.thread && result.user && result.turn) {
          const accepted = result;
          setDetails(previous => ({ ...previous, [accepted.thread!.id]: {
            ...accepted.thread!, messages: [...(previous[accepted.thread!.id]?.messages ?? []).filter(item => item.id !== accepted.user!.id), accepted.user!],
            turns: [...(previous[accepted.thread!.id]?.turns ?? []).filter(turn => turn.id !== accepted.turn!.id), accepted.turn!], activeTurn: accepted.turn,
          } }));
        }
      }
      if (result.status === 'failed') setError(result.error || 'The manager could not hand off this message.');
    } catch (cause) {
      if (currentEpoch === epoch.current && !controller.signal.aborted) {
        setError(errorText(cause));
        // APIError is raised only before opening SSE. A definite 4xx rejection
        // did not accept this submission; retain the editable draft. Lost or
        // truncated streams remain unconfirmed and recover with the same ID.
        if (cause instanceof APIError && cause.status >= 400 && cause.status < 500) {
          setRequests(previous => previous.map(request => request.id === message.requestId ? {
            ...request, status: 'failed', admissionRejected: true,
            result: { requestId: request.id, status: 'failed', events: request.events, error: errorText(cause) },
          } : request));
        }
      }
    } finally {
      if (currentEpoch === epoch.current) { sending.current = false; setBusy(false); }
    }
  }

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!draft.trim() || !sendingProfile || busy || unconfirmed) return;
    void send({ requestId: crypto.randomUUID(), content: draft.trim(), profileName: sendingProfile, ...(subjectID ? { subjectId: subjectID } : {}) });
  };
  const recover = (request: RoutingRequest, explicitSubject?: string) => void send({
    requestId: request.id, content: request.content, profileName: request.profileName,
    ...(explicitSubject || request.subjectId ? { subjectId: explicitSubject || request.subjectId } : {}),
  });
  const selectSubject = (subject: Subject) => {
    setSubjectID(subject.id); setEditing(false); setMergeTarget('');
    setSelectedRequestID([...requests].reverse().find(request => request.subjectId === subject.id || request.result?.subject?.id === subject.id)?.id || '');
  };
  async function saveSubject(patch: Parameters<typeof updateSubject>[3]) {
    if (!selectedSubject || savingSubject) return;
    setSavingSubject(true); setError('');
    const currentEpoch = epoch.current;
    try {
      const updated = await updateSubject(token, namespace, selectedSubject.id, patch);
      if (epoch.current !== currentEpoch) return;
      setSubjects(previous => previous.map(subject => subject.id === updated.id ? updated : subject)); setEditing(false);
    } catch (cause) { if (epoch.current === currentEpoch) setError(errorText(cause)); }
    finally { if (epoch.current === currentEpoch) setSavingSubject(false); }
  }

  async function mergeSubject() {
    if (!selectedSubject || !mergeTarget || savingSubject) return;
    setSavingSubject(true); setError('');
    const currentEpoch = epoch.current;
    try {
      await mergeSubjects(token, namespace, selectedSubject.id, mergeTarget);
      if (epoch.current !== currentEpoch) return;
      setSubjectID(mergeTarget); setMergeTarget('');
      setSelectedRequestID([...requests].reverse().find(request => request.subjectId === mergeTarget || request.result?.subject?.id === mergeTarget)?.id || '');
      await refresh(undefined, false);
    } catch (cause) { if (epoch.current === currentEpoch) setError(errorText(cause)); }
    finally { if (epoch.current === currentEpoch) setSavingSubject(false); }
  }

  if (!enabled || !available) return <section className="manager-unavailable"><DesktopIcon kind="project"/><span className="manager-eyebrow">Central manager</span><h1>Subject routing is not enabled here yet.</h1><p>Your direct conversations remain available in Chat. The manager becomes available when subject routing is enabled for this connection.</p></section>;

  return <section className="manager-page" aria-label="Central manager">
    <header className="manager-header"><div><span className="manager-eyebrow">Central manager</span><h1>Your work, in context.</h1></div><div className="manager-context-controls"><label>Workspace<select aria-label="Manager workspace" value={namespace} disabled={busy} onChange={event => { saveNamespace(event.target.value); setNamespace(event.target.value); }}>{[...new Set([...config.defaultNamespaces, namespace])].map(ns => <option key={ns}>{ns}</option>)}</select></label><label>New subjects use<select aria-label="Agent for new subjects" value={profile} disabled={busy || loading} onChange={event => setProfile(event.target.value)}>{!profiles.length && <option value="">No agent available</option>}{profiles.map(item => <option key={item.metadata.name} value={item.metadata.name}>{item.metadata.name}</option>)}</select></label></div></header>
    {error && <div className="banner banner-error" role="alert">{error}</div>}
    {rosterError && <div className="manager-stream-note" role="status">{rosterError}</div>}
    <div className="manager-workspace">
      <aside className="manager-subjects" aria-label="Your subjects"><div className="manager-panel-heading"><h2>Your subjects</h2><span>{subjects.length}</span><button className="btn btn-ghost" type="button" title="Refresh subjects" aria-label="Refresh subjects" disabled={busy || loading} onClick={() => void refresh().catch(cause => setError(errorText(cause)))}>↻</button></div>
        <button type="button" className={`manager-subject manager-all${!subjectID ? ' is-selected' : ''}`} disabled={busy} onClick={() => { setSubjectID(''); setSelectedRequestID(requests.at(-1)?.id || ''); setEditing(false); }}><DesktopIcon kind="chat"/><span><strong>All subjects</strong><small>Let the manager choose</small></span></button>
        {loading && <p className="manager-panel-note" role="status">Loading your subjects…</p>}
        {!loading && !subjects.length && <p className="manager-panel-note">Subjects will grow from your conversations. Start with an idea or something you are working on.</p>}
        {subjects.map(subject => <button key={subject.id} type="button" className={`manager-subject${subjectID === subject.id ? ' is-selected' : ''}`} disabled={busy} onClick={() => selectSubject(subject)}><DesktopIcon kind={subject.kind === 'conversation' ? 'chat' : 'project'}/><span><strong>{subject.label}</strong><small>{subject.kind} <i aria-hidden="true">·</i> <span className={`subject-status-${subject.status}`}>{subject.status}</span></small></span></button>)}
        {selectedSubject && <section className="manager-subject-detail"><span className="manager-eyebrow">Subject context</span>{editing ? <form onSubmit={event => { event.preventDefault(); void saveSubject({ label: editLabel, summary: editSummary, kind: editKind, aliases: editAliases.split(',').map(alias => alias.trim()).filter(Boolean) }); }}><label>Name<input aria-label="Subject name" value={editLabel} maxLength={200} onChange={event => setEditLabel(event.target.value)} required/></label><label>Kind<select aria-label="Subject kind" value={editKind} onChange={event => setEditKind(event.target.value)}>{['project', 'session', 'preference', 'idea', 'conversation'].map(kind => <option key={kind}>{kind}</option>)}</select></label><label>Also known as<input aria-label="Subject aliases" value={editAliases} onChange={event => setEditAliases(event.target.value)} placeholder="Names separated by commas" maxLength={1000}/></label><label>Summary<textarea aria-label="Subject summary" value={editSummary} maxLength={2000} onChange={event => setEditSummary(event.target.value)}/></label><div className="manager-actions"><button className="btn btn-primary" disabled={savingSubject}>Save</button><button className="btn btn-ghost" type="button" onClick={() => setEditing(false)}>Cancel</button></div></form> : <><p>{selectedSubject.summary || 'No summary yet.'}</p><span className="manager-target-label">{selectedSubject.profileName}</span><div className="manager-actions"><button type="button" className="btn btn-ghost" disabled={savingSubject || busy} onClick={() => { setEditLabel(selectedSubject.label); setEditSummary(selectedSubject.summary); setEditKind(selectedSubject.kind); setEditAliases((selectedSubject.aliases ?? []).join(', ')); setEditing(true); }}>Edit</button><button type="button" className="btn btn-ghost" disabled={savingSubject || busy} onClick={() => void saveSubject({ status: selectedSubject.status === 'open' ? 'parked' : 'open' })}>{selectedSubject.status === 'open' ? 'Park' : 'Reopen'}</button></div>{subjects.length > 1 && <details className="manager-merge"><summary>Merge with another subject</summary><p>Future messages follow the destination. Existing conversations remain saved.</p><label>Destination<select aria-label="Merge destination" value={mergeTarget} disabled={savingSubject || busy} onChange={event => setMergeTarget(event.target.value)}><option value="">Choose a subject</option>{subjects.filter(subject => subject.id !== selectedSubject.id).map(subject => <option key={subject.id} value={subject.id}>{subject.label}</option>)}</select></label><button className="btn btn-ghost" type="button" disabled={!mergeTarget || savingSubject || busy} onClick={() => void mergeSubject()}>Merge into selected subject</button></details>}</>}</section>}
      </aside>
      <div className="manager-conversation"><div className="manager-conversation-heading"><h2>{selectedSubject?.label || 'One conversation. Every subject.'}</h2><p>{selectedSubject ? 'Messages here stay with this subject.' : 'Pick up a project, ask a question, or start something new.'}</p></div>
        <div className="manager-messages" aria-label="Manager conversation">
          {!shownRequests.length && !loading && <div className="manager-welcome"><DesktopIcon kind="forge"/><h3>What is on your mind?</h3><p>The manager connects each message to your recent work. Follow its choices in the live panel.</p></div>}
          {shownRequests.map(request => {
            const result = request.result;
            const detail = details[requestThread(request)];
            const turnID = result?.turn?.id || request.turnId;
            const turn = detail?.turns?.find(item => item.id === turnID) || result?.turn;
            const answer = detail?.messages.find(message => message.role === 'assistant' && (message.metadata as { turnId?: string } | undefined)?.turnId === turnID);
            const decision = [...eventsOf(request)].reverse().find(event => event.decision)?.decision;
            const label = result?.subject?.label || subjects.find(subject => subject.id === request.subjectId)?.label;
            const candidates = [...eventsOf(request)].reverse().find(event => event.candidates)?.candidates ?? [];
            return <article key={request.id} className={`manager-message${selectedRequestID === request.id ? ' is-inspected' : ''}`}>
              <div className="manager-user-message"><span>You <time dateTime={request.createdAt}>{clockTime(request.createdAt)}</time></span><p>{request.content}</p></div>
              <button type="button" className="manager-route-receipt" aria-pressed={selectedRequestID === request.id} onClick={() => setSelectedRequestID(request.id)}><span aria-hidden="true">↳</span><span><strong>{label || (request.status === 'clarify' ? 'Choose a subject' : request.status === 'routing' ? 'Finding the right subject' : 'Routing stopped')}</strong><small>{decision ? intentNames[decision.intent] || decision.intent : request.status === 'routing' ? 'Waiting for routing decisions' : 'Inspect routing steps'}</small></span><span className={`manager-receipt-state state-${request.status}`}>{request.status === 'accepted' ? 'Handed off' : request.status === 'clarify' ? 'Your choice' : request.status === 'routing' ? busy && request.id === selectedRequestID ? 'Live' : 'Unconfirmed' : 'Failed'}</span></button>
              {request.status === 'clarify' && <div className="manager-clarification"><p>{result?.clarification || 'Which subject should this message continue?'}</p><div className="manager-clarification-options">{candidates.filter(candidate => !['new_subject', 'none'].includes(candidate.id)).map(candidate => <button key={candidate.id} className="btn btn-ghost" type="button" disabled={busy} onClick={() => recover(request, candidate.id)}>{candidate.label}</button>)}<button className="btn btn-ghost" type="button" disabled={busy} onClick={() => recover(request, 'new_subject')}>Start a new subject</button><button className="btn btn-ghost" type="button" disabled={busy} onClick={() => recover(request, 'none')}>General conversation</button></div></div>}
              {request.admissionRejected && <p className="manager-turn-status">{request.result?.error} Edit the message or agent selection and send again.</p>}
              {((request.status === 'routing' && !busy) || (request.status === 'failed' && !request.admissionRejected)) && <button type="button" className="btn btn-ghost manager-recover" disabled={busy} onClick={() => recover(request)}>Recover this message</button>}
              {answer && <div className="manager-assistant-message"><span className="manager-eyebrow">{detail?.profileName || 'Assistant'}</span><ChatAssistantBody content={answer.content}/></div>}
              {!answer && liveText && liveText.turnId === turnID && liveText.text && <div className="manager-assistant-message"><span className="manager-eyebrow">Replying</span><ChatAssistantBody content={liveText.text}/></div>}
              {turn && !answer && <div className="manager-turn-status" role="status">{turn.status === 'failed' ? turn.error || 'The agent could not finish this turn.' : turn.status === 'succeeded' ? 'The agent finished. Retrieving the reply…' : turn.status === 'waiting' ? 'Saved for this agent when it becomes available.' : 'Message handed off. Waiting for the agent’s reply.'}</div>}
            </article>;
          })}
          <div ref={transcriptEnd}/>
        </div>
        {streamError && <p className="manager-stream-note" role="status">{streamError}</p>}
        {backgroundUnavailable && <p className="manager-stream-note" role="status">Some other subjects could not be refreshed. Their last saved updates remain visible.</p>}
        <form className="manager-composer" onSubmit={submit}><label className="sr-only" htmlFor="manager-draft">Message the manager</label>{subjectID && <div className="manager-composer-subject"><span>To {selectedSubject?.label}</span><button type="button" disabled={busy} onClick={() => setSubjectID('')}>Let the manager choose ×</button></div>}<textarea id="manager-draft" placeholder="Talk about a project, ask a question, or start something new…" value={draft} disabled={busy || loading} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} rows={3} maxLength={65536}/><div className="manager-composer-footer"><span>{busy ? 'Following routing updates…' : unconfirmed ? 'Recover the unconfirmed message to continue.' : 'Enter to send · Shift + Enter for a new line'}</span><button className="btn btn-primary" disabled={busy || loading || unconfirmed || !draft.trim() || !sendingProfile}>{busy ? 'Routing…' : 'Send ↗'}</button></div></form>
      </div>
      <div className="manager-inspection"><RoutingTimeline request={selectedRequest} subjects={subjects} busy={busy && selectedRequest?.status === 'routing'}/>{selectedTurn && <RemoteTurnActivity token={token} namespace={namespace} turn={selectedTurn} agentLabel={thread?.profileName} recoveryPending={thread?.recoveryPending}/>}</div>
    </div>
  </section>;
}
