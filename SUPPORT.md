# Support

Use GitHub issues for reproducible defects, documentation gaps, and bounded
feature proposals. Include the chart version, image references, Kubernetes
version, architecture, relevant custom resources with secrets removed, and
controller status reasons.

The v1alpha1 project is community-supported without a guaranteed response or
backport window. Consumer deployment questions should identify the external
values or GitOps repository that owns cluster identity, credentials, routing,
storage, placement, and image pins. The repository-local `.hazyforge` build and
test contracts and optional `.hazyforge/clusters/anvil-primaris/` consumer
overlay support Hazy Forge's own deployment; they are examples and maintainer
configuration, not portable operator requirements.

Security reports follow [SECURITY.md](SECURITY.md), never public issues.
