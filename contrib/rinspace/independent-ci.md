# Independent CI host boundary

These public modules prepare independent self-hosted checks when this fork's
Actions dispatcher is unavailable. They do not register an App, relax branch
protection, publish an image, or claim that a native worker has been installed.

`ci-check.sh` is the shared entry for the six existing Go checks. The Actions
matrix and independent plan use the same commands. `github-checks.mjs` is a
**trusted host-side library**, not a script to execute from a PR checkout.
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
node --test contrib/rinspace/ci-check.test.mjs contrib/rinspace/github-checks.test.mjs
```

No installation IDs, source releases or required-check provider IDs are supplied
here. Bind required checks to the real dedicated App only after real candidate
checks succeed. Release provenance and private immutable consumption remain
separate gates; this library cannot fabricate an Actions run or OIDC provenance.
