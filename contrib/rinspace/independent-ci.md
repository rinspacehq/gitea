# Independent CI host boundary

These public modules prepare independent self-hosted checks when this fork's
Actions dispatcher is unavailable. They do not register an App, relax branch
protection, publish an image, or claim that a native worker has been installed.

`ci-check.sh` is the shared entry for the six existing Go checks. The Actions
matrix and independent plan use the same commands. `github-checks.mjs` is a
**trusted host-side library**, not a script to execute from a PR checkout.
The formatting entry also requires these controller/recipe regression tests;
workers must provide Node.js 22 or newer in addition to the pinned Go toolchain.
Install its reviewed immutable version outside the worker and keep it fixed
while candidate commits are tested. Never import controller code from the
candidate, even for same-repository contributions.

The operator provides the dedicated App ID, installation ID, exact App slug and
private key to the trusted controller. The library checks the canonical owner,
identity and exact minimal permissions, then mints a token limited to this one
repository. App registration must use selected repositories, not all repositories.
Before activation, separately verify that the App installation itself selects
only this repository; the token check alone proves only the narrowed token scope.
Do not store keys in this repository, a worker environment, CLI arguments,
public artifacts or logs.

For a reviewed open canonical PR, `runPullCheck` creates an in-progress check at
the full head SHA. Its `execute` callback receives only the SHA, context, step
index and immutable argument array. The callback belongs to the trusted host
controller and must launch a real isolated worker, wait for actual termination
and return `{exitCode, signal, timedOut, cancelled}`. A zero exit code alone is
insufficient. These values must come from the controller's process supervision,
not a JSON receipt written by candidate code.

The controller must enforce deadlines (30 minutes per Go step, 10 minutes per
Docker check), terminate the task's complete process group/container on timeout
or cancellation and wait for cleanup. Never use `Promise.race` as a substitute
for killing a worker. A hung callback leaves the check in progress, not success.
Use a durable single-flight ledger before starting a check. A lost POST/PATCH
response is ambiguous: inspect that same check/external ID before retrying;
do not create replacement checks because an observer timed out.

`native-run.mjs` supplies this durable wrapper and a systemd supervisor. Use
`runNativePullCheck` around the trusted reporter, with `systemdExecutor` as its
executor. The ledger directory must be canonical, controller-owned and mode
0700. Each SHA/context is reserved exclusively; only an unambiguous terminal
result releases the global `active` lock. Exceptions, lost API responses and
uncertain cleanup retain it. Inspect the recorded external/check ID and exact
unit before explicitly reconciling a retained lock. Never delete it based on a
PID file, observation timeout or a guessed stale age, and never overwrite its
original per-SHA evidence to trigger a retry.

The supervisor requires a root-owned, non-writable, SHA-256-pinned launcher,
which receives only SHA, context and step number. Install the launcher and all
its dependencies in a root-owned immutable directory outside the candidate.
It must establish the worker boundary described below, validate the candidate
SHA/clean tree and select only the fixed plan. **The supervisor module alone
does not install a worker.** The optional adapters are
for offline supervisor tests, never supplied by a PR or production worker.

The shared parent slice must enforce CPU quota 50000/100000, CPU 0, 2500 MiB
and no swap. Each Go/Docker client receives a 30/10 minute systemd deadline,
respectively. After observing a terminal result, the supervisor explicitly
stops the entire unit and requires its cgroup to be empty or removed before
returning any successful receipt. SIGINT/SIGTERM and a scratch reserve below
1.5 billion bytes cancel the run. That scratch reserve is not a production
capacity waiver. Abrupt controller loss leaves durable evidence and systemd
deadlines in force, not a successful check.

`native-worker.mjs` is the separate trusted namespace launcher. Install its
reviewed executable and its sibling `github-checks.mjs` outside the candidate.
Its only CLI arguments are the full SHA, context and numeric step. An adjacent
root-owned, mode-0600 `worker-config.json` selects the already prepared public
Go and Docker task roots, their unprivileged users/UIDs, and the dedicated
rootless engine service/state directory. This host configuration is not a
repository file and must not contain App credentials or production mounts.
Both roots must be dedicated `public-gitea-*` directories; mount-point parents
must be root-owned/non-writable, while individual home/cache/scratch directories
are worker-owned. Symlinked mounts are rejected.

Go workers use a readonly source and SDK/modules, writable task-only cache/tmp,
all namespaces, no network, a cleared environment and no capabilities. Docker
workers join only the validated dedicated engine child network, after checking
its exact service cgroup, distinct host network and the outer namespace's lack
of a default route. They receive only the rootless socket directory and readonly
source. Inside both workers the exact clean SHA, UID and zero effective
capabilities are rechecked; Docker also verifies rootless identity/storage.
Missing or mismatched engine state refuses to start; there is no host-engine
fallback. Engine bootstrap/registry egress and canonical candidate preparation
remain host operations, not commands supplied by a candidate.

The library runs steps serially, stops on failure or incomplete results, and
revalidates the PR head before completion. A moved/closed PR is cancelled. It
refuses to overwrite another App, SHA, context, ID or terminal check. Only all
six Go steps or both real BuildKit Dockerfile checks can produce success.

The worker must have a fresh filesystem/user/PID/network boundary, no production
or private mounts, no host Docker socket, no controller key/token, bounded CPU
and memory, and only reviewed public dependency access. Docker checks require a
dedicated rootless engine. Do not silently use the host engine when rootless
prerequisites are unavailable.

Offline transport/recipe regression tests (synthetic key/API/worker, **not real
GitHub CI or Docker checks**):

```sh
node --test contrib/rinspace/ci-check.test.mjs contrib/rinspace/github-checks.test.mjs contrib/rinspace/native-run.test.mjs contrib/rinspace/native-worker.test.mjs
```

No installation IDs, source releases or required-check provider IDs are supplied
here. Bind required checks to the real dedicated App only after real candidate
checks succeed. Release provenance and private immutable consumption remain
separate gates; this library cannot fabricate an Actions run or OIDC provenance.

## Native release workflow

When Actions dispatch is unavailable, `native-build.sh` is the public build
recipe for the same Dockerfile, labels, native amd64 platform and actual
BuildKit SBOM/provenance output. The fixed launcher accepts `native-release`
only when its private host configuration approves that full source commit and
an immutable RC version. Its 3-hour supervisor deadline does not change the
30/10-minute check deadlines or aggregate resource limits. The ordinary PR
reporter refuses this context: a release needs a separate trusted publisher.

The publisher must verify the source is merged into the maintenance line and
both same-SHA required App checks succeeded before and after the build. It
must reserve durable identities, supervise and clean up the complete worker,
derive exact OCI index/platform/config and statement digests, verify the source
labels, and only then sign a receipt and publish a real native release check.
The output is OCI, not a pushed mutable tag. Never pass a registry token or
signing key to `native-build.sh`. Its network=host option refers only to the
validated dedicated engine-child namespace, not the machine network.
The operator must provision the `rinspace-native-release` docker-container
builder inside that same dedicated engine with a reviewed immutable BuildKit
image and serialized build configuration. `native-buildkit.toml` pins the
reviewed upstream v0.34.0 image and limits OCI execution to one vertex at a
time with a native snapshotter. The recipe refuses the default
docker driver, which cannot preserve the required OCI/attestation output.

`native-receipt.mjs` defines a strict signed native statement and DSSE PAE
domain separation. Consumers must pin the RSA public-key SHA-256 and App
identity in independent reviewed policy, not trust values supplied by a new
receipt or lock. Receipts bind source/tree, image digests, recipe/controller,
logs, timestamps, both successful check identities and statement digests.
They contain no fake workflowRunId/jobId or OIDC claim. Signing a statement
is not by itself evidence a build ran: the publisher must obtain each field
from supervised execution and revalidated GitHub/registry facts. Preserve the
raw logs/BuildKit output and publish the signed receipt as a release asset.
Both RC and accepted tags must resolve to the exact same immutable OCI index.

A linear-history rebase merge creates a new maintenance commit identity. Use
`runMaintenanceCheck`, wrapped by `runNativeMaintenanceCheck`, to actually
rerun the original matrices at that exact
protected maintenance head; it verifies the real branch before and after all
steps. Never copy a PR check result onto the new SHA or forge an open PR. A
moved/unprotected head refuses or cancels the check. This endpoint still cannot
run the native publisher: release supervision and signing remain separate.

Offline crypto and recipe tests use synthetic keys/tools only. They do not
constitute a native release, a namespace proof or production acceptance.
