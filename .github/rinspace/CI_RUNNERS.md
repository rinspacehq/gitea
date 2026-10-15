# Rinspace fork-check runner contract

Trusted branches in `rinspacehq/gitea` use the dedicated
`rinspace-gitea-checks` runner label. External fork pull requests retain
GitHub-hosted runners; they must not be approved onto the trusted pool.
The required `focused-go` and `dockerfile-check` checks and their test
commands are unchanged. Matrix work is serial.

The self-hosted pool is ephemeral and dedicated to reviewed immutable
jobs. Provision it separately from product, deployment, and other private
runners, without default shared labels. Constrain the complete task to
half a CPU, one CPU affinity, bounded memory and writable disk, and one
active heavy job. Do not throttle production services or runner listeners.

Public code must not receive private source, production credentials,
repository/runtime data, or the host Docker socket. Restrict filesystem
mounts and network access; allow only the public dependency and Actions
services needed by the reviewed job. Keep runtime binaries and job
admission policy read-only. Remove ephemeral registration after the job.

The Dockerfile job requires its own isolated Docker/BuildKit engine at
`DOCKER_HOST=unix:///work/buildkit/docker.sock`. The socket belongs only
to that disposable build environment, not the host daemon. Missing or
incorrect isolation fails the job; never skip either Dockerfile check or
mount the host Docker socket to make it pass.

Hosted-runner disk cleanup is restricted to `runner.environment ==
'github-hosted'`. Self-hosted capacity is provisioned explicitly; no
host-wide toolchain deletion is allowed.

Changing runner routing does not bypass repository-level Actions
disablement. Before provisioning the pool, verify that GitHub accepts
the reviewed workflow and creates a run. A repository-disabled 422 before
any run exists is an admission failure, not a lack of runners; retain the
exact error and escalate rather than repeatedly registering more runners.
