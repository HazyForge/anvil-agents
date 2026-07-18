# Support

Use GitHub issues for reproducible defects, documentation gaps, and bounded
feature proposals. Include the chart version, image references, Kubernetes
version, architecture, relevant custom resources with secrets removed, and
controller status reasons.

The v1alpha1 project is community-supported without a guaranteed response or
backport window. Consumer deployment questions should identify the external
values or GitOps repository that owns cluster identity, credentials, routing,
storage, placement, and image pins. The `.hazyforge` files in this repository
are maintainer build/test contracts, not a production cluster overlay.

Security reports follow [SECURITY.md](SECURITY.md), never public issues.
