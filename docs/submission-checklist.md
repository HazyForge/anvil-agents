# OpenAI Build Week submission checklist

The [official rules](https://openai.devpost.com/rules) are authoritative. The
deadline is July 21, 2026 at 5:00 p.m. PDT (7:00 p.m. CDT). Do not submit while
any required item below remains open.

## Entrant and ownership

- [ ] Join the hackathon with the submitting Devpost account before the deadline.
- [ ] Confirm every entrant/team member is at least the age of majority where
  they reside and lives in a supported country or territory, not an excluded
  jurisdiction.
- [ ] Confirm no entrant is sponsor/administrator staff or agent or their
  immediate family/household member; a Judge; an organization or individual
  that employs a Judge; an applicable parent, subsidiary, or affiliate; or
  subject to another real/apparent conflict of interest.
- [ ] Confirm individual/team/organization entrant type and that every member is
  separately eligible.
- [ ] If Hazy Forge or a team is the entrant, appoint and authorize the Devpost
  representative.
- [ ] Confirm the entrant solely owns the original submitted work and holds valid
  licenses or authorization for every third-party dependency, integration,
  asset, and trademark use.
- [ ] Confirm the project did not receive prohibited financial or preferential
  support from the sponsor/administrator.

## Repository and evidence

- [ ] Merge only intended submission work and exclude unrelated draft branches.
- [ ] Replace every TODO and placeholder in submission documents.
- [ ] Record a successful authenticated GPT-5.6 AgentRun in the evidence ledger.
- [ ] Run `/feedback` in the original core Codex thread and use the returned
  Session ID; do not assume the candidate ID is the `/feedback` result.
- [ ] Run `make verify` on the exact final revision.
- [ ] Run `./hack/test-judge-kind.sh` from a clean cluster on the exact final
  revision and save the non-secret result.
- [ ] Create an immutable submission tag and keep the repository public with
  Apache-2.0 licensing, or share a private repository with both required judge
  addresses: `testing@devpost.com` and `build-week-event@openai.com`.
- [ ] Publish and pin a chart/controller from that same revision if claiming
  runnable features added after v0.1.1; otherwise narrow the final claims and
  demo to the v0.1.1 runtime contract tested by the judge script.
- [ ] Make the Devpost repository URL point to that immutable revision.
- [ ] Verify all README and judge links from an anonymous browser.

## Description and video

- [ ] Submit in Developer Tools and provide a complete English description.
- [ ] Clearly distinguish the pre-existing foundation from eligible Build Week
  extensions and include dated commit/session evidence.
- [ ] Describe Codex acceleration, GPT-5.6's product contribution, and the human
  product/engineering decisions.
- [ ] Record a clear narrated demo under three minutes showing the actual
  product, a successful GPT-5.6 run, and the Codex collaboration story.
- [ ] Remove secrets, private information, copyrighted music, and unauthorized
  third-party material from every frame and audio track.
- [ ] Upload the final video publicly to YouTube and add its URL to Devpost and
  the repository.

## Testing availability

- [ ] Provide installation instructions, supported platforms, and the free
  no-build judge path from `JUDGING.md`.
- [ ] Keep all public artifacts and testing access free and unrestricted through
  the conservative later public-schedule date of August 9, 2026; the official
  rules currently define the Judging Period through August 5 at 5:00 p.m. PDT.
- [ ] Test anonymous GHCR/chart pulls and the YouTube/repository URLs after the
  final tag is published.
- [ ] Submit before July 21, 2026 at 5:00 p.m. PDT; no substantive changes are
  allowed after the submission period unless expressly permitted.
