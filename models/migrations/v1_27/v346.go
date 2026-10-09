// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import "gitea.dev/models/db"

// EnsureRinControlPublicationEventIDCapacity repairs databases where the
// earlier XORM sync left the existing event_id column at its legacy length.
func EnsureRinControlPublicationEventIDCapacity(x db.EngineMigration) error {
	return ExpandRinControlOutboxForPublicationEvents(x)
}
