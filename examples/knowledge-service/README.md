# External Knowledge Service

This example teaches every built-in harness the same small read-only tool
contract without embedding a knowledge vendor in the operator. The example
expects `GET /v1/search?q=...` to return JSON and reads its bearer token from a
same-namespace Secret.

Adapt the setup wrapper to your deployed API, use a real TLS endpoint, and
replace `knowledge-reader` with a secret containing only read authority. The
example Secret contains a placeholder and must not be committed with a real
token. The wrapper intentionally installs into the run workdir so it does not
need root access.

For Hazy Forge, the same pattern can wrap the separately deployed
`knowledge-based` CLI or service while keeping the Markdown vault, access
policy, and credentials outside this open-source operator.
