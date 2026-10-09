// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rinauth

import (
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordRevocationReceiptIsDurableAndIdempotent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(RevocationReceipt))
	require.NoError(t, err)

	receipt := &RevocationReceipt{
		EventID: "11111111-1111-4111-8111-111111111111", AggregateType: "session",
		AggregateID: "22222222-2222-4222-8222-222222222222", EventType: "session.revoked",
		Version: 2, Payload: `{ "reason": "logout" }`,
	}
	require.NoError(t, RecordRevocationReceipt(t.Context(), receipt))
	require.NoError(t, RecordRevocationReceipt(t.Context(), &RevocationReceipt{
		EventID: receipt.EventID, AggregateType: receipt.AggregateType, AggregateID: receipt.AggregateID,
		EventType: receipt.EventType, Version: receipt.Version, Payload: `{"reason":"logout"}`,
	}))

	count, err := db.GetEngine(t.Context()).Count(new(RevocationReceipt))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	err = RecordRevocationReceipt(t.Context(), &RevocationReceipt{
		EventID: receipt.EventID, AggregateType: receipt.AggregateType, AggregateID: receipt.AggregateID,
		EventType: receipt.EventType, Version: 3, Payload: receipt.Payload,
	})
	assert.ErrorIs(t, err, ErrRevocationReceiptConflict)
}

func TestRecordRevocationReceiptRejectsMalformedEnvelope(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	for _, receipt := range []*RevocationReceipt{
		{EventID: "not-a-uuid", AggregateType: "session", AggregateID: "sid", EventType: "session.revoked", Version: 1, Payload: `{}`},
		{EventID: "11111111-1111-4111-8111-111111111112", AggregateType: "account", AggregateID: "uid", EventType: "session.revoked", Version: 1, Payload: `{}`},
		{EventID: "11111111-1111-4111-8111-111111111113", AggregateType: "account", AggregateID: "uid", EventType: "account.authorization_changed", Version: 0, Payload: `{}`},
		{EventID: "11111111-1111-4111-8111-111111111114", AggregateType: "account", AggregateID: "uid", EventType: "account.authorization_changed", Version: 1, Payload: `{`},
	} {
		assert.Error(t, RecordRevocationReceipt(t.Context(), receipt))
	}
}
