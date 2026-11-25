# Anvil Primaris deployment retired

Anvil Agents and ATE have been withdrawn from the production release path.
This checkout intentionally has no Primaris registration or direct-apply
helper. The portable chart, controller, APIs, and source history are retained.

Use the [credential-free Kind quickstart](getting-started.md#credential-free-kind-run)
and `make kind-e2e` to evaluate changes locally. Use a separate kubeconfig
and synthetic data. An unrelated production release or chart upgrade is
not part of this workflow.
