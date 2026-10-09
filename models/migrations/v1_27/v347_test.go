// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

func TestAddRinControlDeliveryControls(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0)
	defer deferable()
	require.NoError(t, AddRinControlDeliveryControls(x))

	type RinControlDeliveryControl struct {
		Channel    string `xorm:"pk VARCHAR(32)"`
		State      string `xorm:"VARCHAR(16)"`
		ChangeID   string `xorm:"VARCHAR(128)"`
		ReasonCode string `xorm:"VARCHAR(32)"`
		Version    int64
	}
	control := new(RinControlDeliveryControl)
	has, err := x.Table("rin_control_delivery_controls").ID("publication").Get(control)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, "enabled", control.State)
	require.Empty(t, control.ChangeID)
	require.Empty(t, control.ReasonCode)
	require.EqualValues(t, 1, control.Version)

	// Re-running the additive migration must retain the same singleton row.
	require.NoError(t, AddRinControlDeliveryControls(x))
	count, err := x.Table("rin_control_delivery_controls").Count(new(RinControlDeliveryControl))
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
