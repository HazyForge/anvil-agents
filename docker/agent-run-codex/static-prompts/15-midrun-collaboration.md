# Mid-run collaboration (status JSON only)

While your AgentRun Job is still active, you may request a peer or interrupt
duplicate work. The controller creates or fails AgentRuns; never POST runs from
the harness.

Emit a decision log line (see project plan for exact JSON). Actions:
`requestPeer` (fields `peerProfileName`, `peerPrompt`, `summary`) and
`interruptDuplicate` (`duplicateRunName`, `summary`).
