# Rinspace fork release policy

## Source lines

`rinspace-1.27` is the maintained line based on Gitea `v1.27.2`. Published
maintenance-line history is append-only: do not rebase, force-push, move an
upstream tag, or replace an existing release artifact.

Rinspace versions use the following form:

```text
v1.27.2-rinspace.1-rc.1
v1.27.2-rinspace.1
```

The first is a public candidate. The second means the same candidate bytes were
accepted after public and private integration checks. A release note records
production adoption separately; a tag alone does not claim adoption.

## Build and publish

The `rinspace-release` workflow is the only fork workflow allowed to publish a
container. It is manual and accepts a full reviewed source commit plus a version.
It verifies that the commit belongs to `rinspace-1.27`, builds from public source,
refuses to overwrite an existing version tag, and publishes only to
`ghcr.io/rinspacehq/gitea/runtime`. This separate public package avoids exposing
the historical private `ghcr.io/rinspacehq/gitea` package or its build layers.

The initial workflow builds `linux/amd64`, the current Rinspace production
platform. Adding another platform changes the reviewed release scope and requires
its own image-start and Git transport checks.

The Dockerfiles pin their public build and runtime image indexes by digest. The
workflow emits BuildKit SBOM and provenance attestations and records the resulting
image digest in its run summary. It never uploads to the Gitea project's Docker
Hub, S3/R2, or Snapcraft destinations.

Upstream release and bot workflows are retained for history and future merges,
but their jobs are restricted to `go-gitea/gitea`; they must remain disabled in
this fork.

## Acceptance and private consumption

Before a candidate can be accepted, verify from an anonymous client that its
repository and GHCR manifest are public. Then use that exact digest for isolated
Rinspace integration checks covering:

- PostgreSQL migration from the supported source schema;
- Web, HTTP Git, and SSH Git startup and transport;
- identity activation, revocation, and negative authorization;
- publication capture, retry, signed delivery, pause/resume, and heartbeat;
- repository presentation and Star/Watch/Follow behavior;
- Tags and code-server consumers;
- rollback to the recorded old digest within the documented schema boundary.

The private Rinspace repository stores the accepted public commit, upstream base,
version, manifest/platform digest, SBOM reference, and migration head in one
release lock. Production consumes only that lock. It does not apply the old
private patch or rebuild the public image.

## Upstream updates

Configure `origin` for `rinspacehq/gitea` and the official Gitea repository as a
fetch-only `upstream`. Bring upstream fixes into a published maintenance line by
reviewed merge or cherry-pick with their original commit reference. Create a new
`rinspace-<major>.<minor>` branch for a Gitea minor-version upgrade and repeat the
migration, integration, and release checks.
