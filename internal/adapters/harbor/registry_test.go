package harbor

import (
	"testing"
	"time"

	sdkmodels "github.com/goharbor/go-client/pkg/sdk/v2.0/models"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssembleProjectsBuildsCorrectTree is a characterization test for
// assembleProjects: it pins down the current (correct) behavior before the
// function is rewritten from an O(projects*repos + repos*artifacts) nested
// scan to a map-indexed O(projects+repos+artifacts) build, so the rewrite
// can be checked against it.
func TestAssembleProjectsBuildsCorrectTree(t *testing.T) {
	pushTime := strfmt.DateTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

	projs := []*sdkmodels.Project{
		{ProjectID: 1, Name: "proj-a", RepoCount: 1},
		{ProjectID: 2, Name: "proj-b", RepoCount: 1},
	}
	repos := []*sdkmodels.Repository{
		{ID: 10, ProjectID: 1, Name: "proj-a/repo-a", ArtifactCount: 2},
		{ID: 20, ProjectID: 2, Name: "proj-b/repo-b", ArtifactCount: 0},
	}
	artifacts := []*sdkmodels.Artifact{
		{RepositoryID: 10, Digest: "sha256:one", Size: 100, PushTime: pushTime},
		{RepositoryID: 10, Digest: "sha256:two", Size: 200, PushTime: pushTime, Tags: []*sdkmodels.Tag{
			{Name: "latest", PushTime: pushTime},
		}},
		// belongs to no repo in this batch - must not leak into repo-a or repo-b.
		{RepositoryID: 999, Digest: "sha256:orphan", Size: 300, PushTime: pushTime},
	}

	got := assembleProjects(projs, repos, artifacts)

	require.Len(t, got, 2)

	projA := got[0]
	assert.Equal(t, "proj-a", projA.Name)
	require.Len(t, projA.Repos, 1)
	repoA := projA.Repos[0]
	assert.Equal(t, "proj-a/repo-a", repoA.Name)
	assert.Equal(t, "repo-a", repoA.NameWithinProject)
	assert.Same(t, projA, repoA.Project)
	require.Len(t, repoA.Artifacts, 2)
	digests := []string{repoA.Artifacts[0].Digest, repoA.Artifacts[1].Digest}
	assert.ElementsMatch(t, []string{"sha256:one", "sha256:two"}, digests)
	for _, art := range repoA.Artifacts {
		assert.Same(t, repoA, art.Repo)
		if art.Digest == "sha256:two" {
			require.Len(t, art.Tags, 1)
			assert.Equal(t, "latest", art.Tags[0].Name)
		}
	}

	projB := got[1]
	assert.Equal(t, "proj-b", projB.Name)
	require.Len(t, projB.Repos, 1)
	assert.Empty(t, projB.Repos[0].Artifacts)
}
