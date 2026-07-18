# Security Policy

## Supported Versions

Anvil Agents is currently pre-1.0. Security fixes are applied to the newest
release line and `master`; older alpha versions are not guaranteed patches.

## Reporting A Vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub private
vulnerability reporting for `HazyForge/anvil-agents`. If that feature is not
available, contact the Hazy Forge repository owners through the security
contact listed on the GitHub organization profile.

Include affected versions, deployment assumptions, impact, reproduction steps,
and any proposed mitigation. Do not access data or infrastructure you do not
own and do not include real credentials in the report.

The maintainers will acknowledge a complete report as soon as practical,
coordinate validation and remediation, and credit reporters who request it.
Disclosure timing is coordinated after a fix or mitigation is available.

## Deployment Responsibility

Permission to author `AgentRun` or `AgentRunProfile` resources grants powerful
workload execution and credential-selection capability. Read
`docs/security.md` before exposing those APIs to another trust domain.
