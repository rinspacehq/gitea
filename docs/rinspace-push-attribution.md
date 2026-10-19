# Managed push attribution

`RIN_CONTROL_PUSH_ATTRIBUTION_ENABLED=true` adds optional positive `data.pusherUserId`
to managed `repository.push.completed` events. The value is the authenticated
post-receive `PushUpdateOptions.PusherID` (from `HookOptions.UserID`), not Git
author/committer names, email addresses, repository files or push options.

Default is disabled. Deploy a reviewed Control Plane consumer that accepts and
durably audits this field **before** enabling the producer. With the flag disabled,
legacy event bytes omit the field; identity remains unknown. Enabling it does not
backfill old events, grant repository permissions, suspend accounts or revert work.

The outbox stores the same stable actor ID. A repeated attributed tip with a
different actor or `before` commit is an idempotency conflict, never a successful
re-attribution. Existing deterministic repository/ref/tip event identities do not
fully represent force-push cycles; these require incident review rather than
inferring missing receives. Keep normal branch protections and do not use this
metadata alone to authorize bulk reversal.

This is audit support only, not a complete anti-vandalism or user-reversal API.
Product administration, per-user write containment, reviewed conflict handling,
ordinary revert commits and exact publication completion belong to the host.
