// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"strings"
	"testing"

	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/git"
	repo_module "gitea.dev/modules/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRinspacePublicationPushEvents(t *testing.T) {
	t.Setenv("RIN_CONTROL_PUBLICATION_EVENT_REPOSITORY_IDS", "none")
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	_, _ = db.GetEngine(ctx).Where("event_type = ?", "repository.push.completed").Delete(new(rincontrol_model.OutboxEvent))
	_, _ = db.DeleteByID[repo_model.RinspaceRepositoryPresentation](ctx, repo.ID)
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = db.GetEngine(cleanupContext).Where("event_type = ?", "repository.push.completed").Delete(new(rincontrol_model.OutboxEvent))
		_, _ = db.DeleteByID[repo_model.RinspaceRepositoryPresentation](cleanupContext, repo.ID)
	})
	applied, err := repo_model.UpsertRinspaceRepositoryPresentation(ctx, &repo_model.RinspaceRepositoryPresentation{
		RepoID: repo.ID, Title: "Managed article", ContentType: repo_model.RinspaceContentArticle, Version: 1,
	}, nil)
	require.NoError(t, err)
	require.True(t, applied)

	zero := strings.Repeat("0", 40)
	before := strings.Repeat("a", 40)
	after := strings.Repeat("b", 40)
	preview := strings.Repeat("c", 40)
	updates := []*repo_module.PushUpdateOptions{
		{RefFullName: git.RefNameFromBranch("main"), OldCommitID: before, NewCommitID: after},
		{RefFullName: git.RefNameFromBranch("preview"), OldCommitID: after, NewCommitID: preview},
		{RefFullName: git.RefNameFromTag("v1"), OldCommitID: zero, NewCommitID: after},
		{RefFullName: git.RefNameFromBranch("deleted"), OldCommitID: before, NewCommitID: zero},
		{RefFullName: git.RefNameFromBranch("unchanged"), OldCommitID: after, NewCommitID: after},
	}
	require.NoError(t, EnqueueRinspacePublicationPushEvents(ctx, repo, updates))
	require.NoError(t, EnqueueRinspacePublicationPushEvents(ctx, repo, updates))

	count, err := db.GetEngine(ctx).Where("event_type = ? AND repository_id = ?", "repository.push.completed", repo.ID).Count(new(rincontrol_model.OutboxEvent))
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)

	_, err = db.Exec(ctx, `CREATE TRIGGER rin_control_publication_outbox_failure BEFORE INSERT ON rin_control_outbox BEGIN SELECT RAISE(FAIL, 'injected publication outbox failure'); END`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DROP TRIGGER IF EXISTS rin_control_publication_outbox_failure`)
	})
	err = EnqueueRinspacePublicationPushEvents(ctx, repo, []*repo_module.PushUpdateOptions{{
		RefFullName: git.RefNameFromBranch("main"), OldCommitID: after, NewCommitID: strings.Repeat("d", 40),
	}})
	assert.Error(t, err)
	_, err = db.Exec(ctx, `DROP TRIGGER rin_control_publication_outbox_failure`)
	require.NoError(t, err)
}

func TestEnqueueRinspacePublicationPushEventsIgnoresUnmanagedRepository(t *testing.T) {
	t.Setenv("RIN_CONTROL_PUBLICATION_EVENT_REPOSITORY_IDS", "all")
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	_, _ = db.DeleteByID[repo_model.RinspaceRepositoryPresentation](t.Context(), repo.ID)
	updates := []*repo_module.PushUpdateOptions{{
		RefFullName: git.RefNameFromBranch("main"),
		OldCommitID: strings.Repeat("a", 40), NewCommitID: strings.Repeat("b", 40),
	}}
	require.NoError(t, EnqueueRinspacePublicationPushEvents(t.Context(), repo, updates))
	count, err := db.GetEngine(t.Context()).Where("event_type = ? AND repository_id = ?", "repository.push.completed", repo.ID).Count(new(rincontrol_model.OutboxEvent))
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestSupportedRinspacePublicationType(t *testing.T) {
	for _, contentType := range []string{
		repo_model.RinspaceContentArticle,
		repo_model.RinspaceContentBook,
		repo_model.RinspaceContentPDF,
		repo_model.RinspaceContentTagWiki,
	} {
		assert.True(t, isSupportedRinspacePublicationType(contentType))
	}
	assert.False(t, isSupportedRinspacePublicationType(""))
	assert.False(t, isSupportedRinspacePublicationType("unsupported"))
}
