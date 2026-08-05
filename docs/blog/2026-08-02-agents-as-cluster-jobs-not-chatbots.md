# Agents as Cluster Jobs, Not Chatbots

*A practical composition model for durable, schedulable agent work.*

An in-app chatbot and a cluster-native agent may both call a language model,
but they solve different problems. A chatbot handles a tenant-scoped question
in seconds, close to product data and product permissions. Its state is a
conversation, and retrying usually means sending another message.

Some work needs a different boundary: a long test run, a multi-file change, a
repository audit, or a recurring maintenance pass. It needs explicit identity,
capacity, isolation, and an execution record that remains useful after the
conversation is gone. Anvil Agents treats that work as a Kubernetes workload:
one `AgentRun`, one Job, and one durable result. A new attempt is a new run.

The important design choice is not the model provider. It is the set of small
objects that describe what the work is, how it runs, and when it should start.

## The abstractions

`AgentRun` is the unit of execution. It is append-only: the controller resolves
the requested composition, materializes the run payload, creates its Job, and
records status and evidence. A failed run is not silently rewritten or moved
to another Job. Repeating the work creates another `AgentRun`, preserving the
history of both attempts.

`AgentRunProfile` is the reusable role. It can define scope, policy, standing
prompt, intent, notifications, and the composition selected for that role. A
profile might describe “the Northline backlog worker” or “the documentation
steward,” without embedding the credentials or pod placement needed to execute
either role.

`AgentHarnessProfile` is the execution envelope. It selects the backend and
image, then supplies the Kubernetes identity, credentials, storage, resource
limits, placement, and timeout. Keeping this boundary separate means a team
can change the harness for a role without changing the role’s instructions or
repository scope.

`AgentSkillSet` is a reusable, backend-neutral instruction pack. Skills explain
when and how to perform work, and may include delegated personas. They do not
grant Secrets, ServiceAccounts, storage, or placement. A skill set can teach a
worker how Northline’s issue policy works without deciding which runtime is
allowed to access the repository.

`AgentToolSet` is the independent contract for external tools. It describes
the tools to materialize, including setup and verification, while leaving the
external service, credentials, identity, networking, storage, and placement
to the consuming harness. The distinction is useful: a skill teaches an agent
when to use a client; a tool set installs and proves that client is available.

`AgentSchedule` supplies cadence. It creates append-only runs from a template
at an interval, with controls for suspension, concurrency, named templates,
and backoff after failures or human-required runs. A schedule is therefore a
launch policy, not another kind of agent and not a replacement for a profile.

Together, these objects keep change local. A new instruction changes a
`SkillSet`; a new CLI changes a `ToolSet`; a larger test pass changes a
`HarnessProfile`; a new role changes a `Profile`; a different cadence changes
a `Schedule`.

## A small Northline example

Northline is a fictional fulfillment SaaS with a monorepo. Its customer
console may have a chatbot that answers “where is my order?” using tenant
permissions and a short-lived request. Its engineering platform can instead
run a scheduled worker that selects the oldest eligible issue, makes one
bounded change, runs the relevant tests, and opens a draft pull request for
human review.

The worker’s profile composes the role, instructions, tools, and runtime without
copying a Job manifest:

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRunProfile
metadata:
  name: northline-backlog-worker
  namespace: agents
spec:
  harnessProfileRef:
    name: codex-standard
  skillSets:
    refs:
      - name: backlog-issue-implement
      - name: northline-repository-policy
  toolSets:
    refs:
      - name: github-cli
  harness:
    intent: proposeChange
```

An `AgentSchedule` can launch that profile hourly, with `Forbid` concurrency so
one slow test run does not overlap the next. A second profile can reuse the
same harness and repository-policy skill set for a daily docs pass. Neither
lane needs a chat session to remain open, and neither needs credentials hidden
inside an instruction document.

## Resolution is part of the record

When a human or schedule creates a run, the controller resolves the selected
profile, harness, skill sets, and tool sets in the run’s namespace. It records
the resolved references and digests, materializes the prompt and capability
payload, and creates the Job. The harness then checks out the declared scope,
verifies tools, and executes the intent.

This makes the boundary between a chatbot and a cluster job concrete. The
chatbot is optimized for interaction with a user. The cluster job is
optimized for repeatability, resource scheduling, least-privilege identity,
and evidence. Both can exist in the same product, but they should not share an
ambient authority model merely because both are called agents.

## AgentCouncil: naming a workforce

The highest-level composition object is `AgentCouncil`: a durable name for a
reusable multi-agent team or workforce. Its members are an inventory of roles,
each pointing to an `AgentRunProfile` in the same namespace. The inventory says
which profiles belong to the workforce; it does not change how an individual
run is executed.

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentCouncil
metadata:
  name: northline-release-workforce
  namespace: agents
spec:
  members:
    - role: coordinator
      profileRef: { name: release-coordinator }
    - role: verifier
      profileRef: { name: release-verifier }
  councilPrompt: |
    Coordinate through explicit artifacts and report evidence to the operator.
```

Association is opt-in. An executing `AgentRunProfile` or `AgentRun` sets
`councilRef` to select the workforce. If the council has a non-empty
`councilPrompt`, the controller optionally materializes that text as a skill
named `council-<council-name>`. An empty prompt records the association without
injecting instructions, and membership alone never injects the prompt into
member runs.

An `AgentCouncil` does not create a multi-agent Job, broker a shared live
conversation, or grant Secrets and ServiceAccounts. The controller still runs
one harness adapter per `AgentRun`; coordination remains an explicit behavior
of the selected profiles and their permitted tools. The council gives that
reusable workforce a name without smuggling execution authority into the
inventory.

That is the pattern end to end: roles compose skills and tools; harnesses
own how a role executes; schedules create runs; councils name the teams you
reuse. Chat stays in the product. Durable agent work becomes cluster Jobs
with records you can inspect later.

Start with [Getting started](../getting-started.md) or the [docs index](../).
