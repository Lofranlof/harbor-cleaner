package app

import (
	"context"
	"testing"

	"harbor-cleaner/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRegistry is an in-memory ports.ArtifactRegistry used to test Cleaner's
// orchestration without a real Harbor.
type fakeRegistry struct {
	projects []*domain.Project

	fakeDeleted []string
	hardDeleted []string
	moved       []string
}

func (f *fakeRegistry) ListAllProjects(ctx context.Context) ([]*domain.Project, error) {
	return f.projects, nil
}

func (f *fakeRegistry) ListProjects(ctx context.Context, names []string) ([]*domain.Project, error) {
	filtered := make([]*domain.Project, 0, len(names))
	for _, p := range f.projects {
		for _, name := range names {
			if p.Name == name {
				filtered = append(filtered, p)
			}
		}
	}
	return filtered, nil
}

func (f *fakeRegistry) FakeDeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	f.fakeDeleted = append(f.fakeDeleted, digest)
	return nil
}

func (f *fakeRegistry) DeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	f.hardDeleted = append(f.hardDeleted, digest)
	return nil
}

func (f *fakeRegistry) MoveArtifact(ctx context.Context, sourceProjectName, targetProjectName, repoName, digest string) error {
	f.moved = append(f.moved, digest)
	return nil
}

// fakeWorkload is an in-memory ports.WorkloadSource.
type fakeWorkload struct {
	refs map[string]struct{}
}

func (f *fakeWorkload) LiveImageRefs(ctx context.Context) (map[string]struct{}, error) {
	return f.refs, nil
}

// buildProject wires up a Project -> Repository -> Artifact tree with back-references.
func buildProject(name, repoName string, artifacts ...*domain.Artifact) *domain.Project {
	project := &domain.Project{Name: name}
	repo := &domain.Repository{
		Project:           project,
		Name:              name + "/" + repoName,
		NameWithinProject: repoName,
		Artifacts:         artifacts,
	}
	for _, art := range artifacts {
		art.Repo = repo
	}
	project.Repos = []*domain.Repository{repo}
	return project
}

func TestCleanerDryRunDeletesOnlyUnpreserved(t *testing.T) {
	fresh := &domain.Artifact{Digest: "sha256:fresh", AgeHours: 1}
	stale := &domain.Artifact{Digest: "sha256:stale", AgeHours: 10000}
	registry := &fakeRegistry{projects: []*domain.Project{buildProject("proj", "repo", fresh, stale)}}

	cleaner := NewCleaner(Options{
		ProjectsToClean:      []string{"all"},
		ReposToPreserve:      []string{"none"},
		ProjectsToPreserve:   []string{"none"},
		PushedDaysAgo:        30,
		TopAge:               0,
		DeleteMode:           "dry-run",
		RegistryHost:         "registry.example.com",
		NumOfWorkersCleaning: 4,
	}, registry, nil)

	ctx := context.Background()
	require.NoError(t, cleaner.Collect(ctx))
	require.NoError(t, cleaner.Preserve(ctx))
	require.NoError(t, cleaner.Clean(ctx))

	assert.True(t, fresh.Preserve)
	assert.False(t, stale.Preserve)
	assert.ElementsMatch(t, []string{"sha256:stale"}, registry.fakeDeleted)
	assert.Empty(t, registry.hardDeleted)
	assert.Empty(t, registry.moved)
}

func TestCleanerSoftDeleteMovesInsteadOfDeleting(t *testing.T) {
	stale := &domain.Artifact{Digest: "sha256:stale", AgeHours: 10000}
	registry := &fakeRegistry{projects: []*domain.Project{buildProject("proj", "repo", stale)}}

	cleaner := NewCleaner(Options{
		ProjectsToClean:      []string{"all"},
		ReposToPreserve:      []string{"none"},
		ProjectsToPreserve:   []string{"none"},
		PushedDaysAgo:        9,
		TopAge:               0,
		DeleteMode:           "soft-delete",
		GarbageProjectName:   "trashcan",
		RegistryHost:         "registry.example.com",
		NumOfWorkersCleaning: 4,
	}, registry, nil)

	ctx := context.Background()
	require.NoError(t, cleaner.Collect(ctx))
	require.NoError(t, cleaner.Preserve(ctx))
	require.NoError(t, cleaner.Clean(ctx))

	assert.ElementsMatch(t, []string{"sha256:stale"}, registry.moved)
	assert.Empty(t, registry.hardDeleted)
	assert.Empty(t, registry.fakeDeleted)
}

func TestCleanerPreservesLiveWorkloadImages(t *testing.T) {
	live := &domain.Artifact{
		Digest:   "sha256:live",
		AgeHours: 10000,
		Tags:     []domain.Tag{{Name: "v1"}},
	}
	dead := &domain.Artifact{Digest: "sha256:dead", AgeHours: 10000}
	registry := &fakeRegistry{projects: []*domain.Project{buildProject("proj", "repo", live, dead)}}
	workload := &fakeWorkload{refs: map[string]struct{}{
		"registry.example.com/proj/repo:v1": {},
	}}

	cleaner := NewCleaner(Options{
		ProjectsToClean:      []string{"all"},
		ReposToPreserve:      []string{"none"},
		ProjectsToPreserve:   []string{"none"},
		PushedDaysAgo:        9,
		TopAge:               0,
		DeleteMode:           "dry-run",
		RegistryHost:         "registry.example.com",
		NumOfWorkersCleaning: 4,
	}, registry, workload)

	ctx := context.Background()
	require.NoError(t, cleaner.Collect(ctx))
	require.NoError(t, cleaner.Preserve(ctx))

	assert.True(t, live.Preserve)
	assert.False(t, dead.Preserve)
}
