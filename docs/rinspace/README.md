# Rinspace Gitea fork

This repository is Rinspace's maintained fork of
[`go-gitea/gitea`](https://github.com/go-gitea/gitea). The current line starts
from Gitea `v1.27.2` at
`1dac1bb2f8593d4319125fa6bca9283000a2ddc2`. It retains the complete upstream
history and MIT License.

`v1.27.2-rinspace.1` is the first accepted fork release. It points to public
source commit `6b3025fa5c4bfe2666773d021432556237a97156` and promotes the exact
candidate OCI index
`sha256:46a1928a68d09bc482c6a1bda6d1a3b68564cfe965be66e0db74eedbadba8026`
after public and private integration checks. The release does not by itself
claim that a production deployment has switched to this image.

The fork adds four integration areas:

- a strict credential gate backed by the Rinspace identity service;
- stable repository presentation metadata without changing Git clone identity;
- explicit Star, Watch, and Follow state used by Rinspace clients;
- a transactional publication outbox, signed delivery, heartbeat, health, and
  maintenance controls for a separate Control Plane.

These additions do not embed the Rinspace application, Tags service, renderer,
production configuration, or user data in Gitea.

## Ordinary Gitea mode

A normal Gitea installation needs no Rinspace account or service. If the
environment variables below are absent, the identity gate and event dispatcher
remain disabled. The additional database tables and API fields are inert.

Use the upstream [installation documentation](https://docs.gitea.com/) for the
standard Gitea configuration, storage, database, SSH, and backup procedures.

## Rinspace integration

Integration is intentionally configured through runtime environment variables.
Do not put their values in Git, image layers, support bundles, or logs.

| Variable | Purpose |
| --- | --- |
| `RINSPACE_IDENTITY_STRICT` | Set to the exact value `true` to enable the credential gate. |
| `RINSPACE_IDENTITY_INTERNAL_URL` | Base URL of the identity service's private API. |
| `RINSPACE_IDENTITY_SERVICE_ID` | Calling service identity. |
| `RINSPACE_IDENTITY_AUDIENCE` | Expected credential audience. |
| `RINSPACE_IDENTITY_SERVICE_KEY_ID` | Active signing-key identifier. |
| `RINSPACE_IDENTITY_SERVICE_SECRET` | Service signing secret. |
| `RIN_CONTROL_EVENT_ENDPOINT` | Fixed publication-event receiver URL. |
| `RIN_CONTROL_GITEA_EVENT_HMAC_KEY` | Event-delivery key, at least 32 bytes. |
| `RIN_CONTROL_GITEA_HEALTH_HMAC_KEY` | Separate health-read key, at least 32 bytes. |
| `RIN_CONTROL_GITEA_MAINTENANCE_HMAC_KEY` | Separate pause/resume key, at least 32 bytes when maintenance mutation is used. |
| `RINSPACE_REVISION` | Immutable public source revision reported by heartbeat events. |

Setting only part of the event-delivery configuration fails startup. The event
and health keys must be purpose-specific. Identity strict mode also fails closed
when its service configuration is missing or invalid.

The Control Plane remains a separate service. Its endpoint contract, secret
distribution, alerts, and production policy belong to the consuming deployment.

## Data and migrations

The first Rinspace line adds Gitea migrations 343 through 348. Test upgrades on
a database copy before adopting a release, keep a current backup, and allow only
one instance to perform schema migration. A release note must state its migration
head and any rollback limit.

No database, repository volume, account, production hostname, or secret is part
of this source repository.

## Images and consumption

Release images are published as `ghcr.io/rinspacehq/gitea/runtime`. A deployment should
record all of the following together:

- the public source commit and upstream base;
- the Rinspace release version;
- the OCI manifest and platform digest;
- the SBOM/provenance reference;
- the expected migration head.

Use the complete digest in deployment configuration. Branches, pull requests,
`latest`, and version tags are discovery aids and are not immutable consumption
inputs. See [release.md](release.md) for the release and upstream-sync policy.

## Contributing

Follow Gitea's existing [contribution guide](../../CONTRIBUTING.md), coding
style, DCO, and MIT licensing. Make general Gitea fixes suitable for upstream
independent of Rinspace-specific protocol changes. Keep Rinspace integration
explicit, default-off, and covered by focused tests.

Security issues in Rinspace-specific behavior should be reported through the
security contact configured on the `rinspacehq/gitea` repository. Upstream
Gitea vulnerabilities should also follow the upstream project's security policy.
