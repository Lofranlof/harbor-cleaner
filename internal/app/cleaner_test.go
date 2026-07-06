package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"harbor-cleaner/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRegistry is an in-memory ports.ArtifactRegistry used to test Cleaner's
// orchestration without a real Harbor.
type fakeRegistry struct {
	projects []*domain.Project

	mu          sync.Mutex
	fakeDeleted []string
	hardDeleted []string
	moved       []string

	// deleteErr, if set, is returned by DeleteArtifact for every call instead
	// of recording a successful deletion.
	deleteErr error
	// onDelete, if set, is called synchronously at the start of every
	// DeleteArtifact call - used to observe concurrency.
	onDelete func()
}

// fakeStatusError mimics the shape every per-endpoint error type the Harbor
// SDK generates (go-swagger codegen: IsCode(code int) bool), without pulling
// in the real SDK - the app layer must stay decoupled from adapter types.
type fakeStatusError struct {
	msg  string
	code int
}

func (e *fakeStatusError) Error() string        { return e.msg }
func (e *fakeStatusError) IsCode(code int) bool { return code == e.code }

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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fakeDeleted = append(f.fakeDeleted, digest)
	return nil
}

func (f *fakeRegistry) DeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	if f.onDelete != nil {
		f.onDelete()
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hardDeleted = append(f.hardDeleted, digest)
	return nil
}

func (f *fakeRegistry) MoveArtifact(ctx context.Context, sourceProjectName, targetProjectName, repoName, digest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func TestCleanerProjectsReturnsCollectedTree(t *testing.T) {
	registry := &fakeRegistry{projects: []*domain.Project{buildProject("proj", "repo")}}
	cleaner := NewCleaner(Options{ProjectsToClean: []string{"all"}}, registry, nil)

	require.NoError(t, cleaner.Collect(context.Background()))

	assert.Equal(t, registry.projects, cleaner.Projects())
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

// A Harbor 504 can arrive after the artifact was already deleted server-side;
// the retried DeleteArtifact then gets a 404. That must be treated as success,
// detected by the SDK response's typed IsCode method - not by grepping the
// error text, which silently stops working if the message format ever changes.
// The message here deliberately contains no "404"/"400" digit sequence.
func TestCleanTreatsTypedNotFoundAsAlreadyResolved(t *testing.T) {
	stale := &domain.Artifact{Digest: "sha256:stale", AgeHours: 10000}
	registry := &fakeRegistry{
		projects:  []*domain.Project{buildProject("proj", "repo", stale)},
		deleteErr: &fakeStatusError{msg: "artifact vanished before deletion completed", code: 404},
	}

	cleaner := NewCleaner(Options{
		ProjectsToClean:      []string{"all"},
		ReposToPreserve:      []string{"none"},
		ProjectsToPreserve:   []string{"none"},
		PushedDaysAgo:        9,
		TopAge:               0,
		DeleteMode:           "hard-delete",
		RegistryHost:         "registry.example.com",
		NumOfWorkersCleaning: 4,
	}, registry, nil)

	ctx := context.Background()
	require.NoError(t, cleaner.Collect(ctx))
	require.NoError(t, cleaner.Preserve(ctx))

	assert.NoError(t, cleaner.Clean(ctx))
}

// Clean must report every artifact that failed to delete, not just the first
// one it happened to see - otherwise an operator has no idea whether 1 or
// 1000 artifacts failed.
func TestCleanReportsCountOfAllFailedArtifacts(t *testing.T) {
	a := &domain.Artifact{Digest: "sha256:a", AgeHours: 10000}
	b := &domain.Artifact{Digest: "sha256:b", AgeHours: 10000}
	c := &domain.Artifact{Digest: "sha256:c", AgeHours: 10000}
	registry := &fakeRegistry{
		projects:  []*domain.Project{buildProject("proj", "repo", a, b, c)},
		deleteErr: errors.New("boom"),
	}

	cleaner := NewCleaner(Options{
		ProjectsToClean:      []string{"all"},
		ReposToPreserve:      []string{"none"},
		ProjectsToPreserve:   []string{"none"},
		PushedDaysAgo:        9,
		TopAge:               0,
		DeleteMode:           "hard-delete",
		RegistryHost:         "registry.example.com",
		NumOfWorkersCleaning: 4,
	}, registry, nil)

	ctx := context.Background()
	require.NoError(t, cleaner.Collect(ctx))
	require.NoError(t, cleaner.Preserve(ctx))

	err := cleaner.Clean(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3 artifact")
}

// Clean must never run more than NumOfWorkersCleaning deletions concurrently,
// regardless of how it implements the bound internally.
func TestCleanRespectsWorkerCount(t *testing.T) {
	const numWorkers = 3
	artifacts := make([]*domain.Artifact, 20)
	for i := range artifacts {
		artifacts[i] = &domain.Artifact{Digest: fmt.Sprintf("sha256:%d", i), AgeHours: 10000}
	}

	var inFlight, maxInFlight int64
	registry := &fakeRegistry{
		projects: []*domain.Project{buildProject("proj", "repo", artifacts...)},
		onDelete: func() {
			cur := atomic.AddInt64(&inFlight, 1)
			for {
				m := atomic.LoadInt64(&maxInFlight)
				if cur <= m || atomic.CompareAndSwapInt64(&maxInFlight, m, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
		},
	}

	cleaner := NewCleaner(Options{
		ProjectsToClean:      []string{"all"},
		ReposToPreserve:      []string{"none"},
		ProjectsToPreserve:   []string{"none"},
		PushedDaysAgo:        9,
		TopAge:               0,
		DeleteMode:           "hard-delete",
		RegistryHost:         "registry.example.com",
		NumOfWorkersCleaning: numWorkers,
	}, registry, nil)

	ctx := context.Background()
	require.NoError(t, cleaner.Collect(ctx))
	require.NoError(t, cleaner.Preserve(ctx))
	require.NoError(t, cleaner.Clean(ctx))

	assert.LessOrEqual(t, atomic.LoadInt64(&maxInFlight), int64(numWorkers))
}
