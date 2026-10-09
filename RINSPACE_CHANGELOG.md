# Rinspace fork changelog

This file records changes maintained by Rinspace in addition to the upstream
[Gitea changelog](CHANGELOG.md). Release notes distinguish public candidates,
private integration acceptance, and production adoption.

## v1.27.2-rinspace.1 — source accepted, production pending

- Add strict Rinspace identity and credential lifecycle integration.
- Add repository presentation metadata while preserving stable Git names.
- Expose explicit repository Star/Watch and user Follow relationship state.
- Add a transactional publication outbox with signed Control Plane delivery,
  retries, delivery maintenance, health reporting, and heartbeat events.
- Add schema migrations 343 through 348 and focused regression coverage.
- Build the complete frontend and backend from public source using digest-pinned
  public base images.
- Add a manual, exact-commit GHCR release path with SBOM and provenance.

The exact `v1.27.2-rinspace.1-rc.1` source and OCI index passed public checks,
private integration, Git/SSH/Identity/Tags/code-session acceptance and rollback
rehearsal. `v1.27.2-rinspace.1` promotes those same bytes without rebuilding.
Production adoption is tracked separately and is still pending.
