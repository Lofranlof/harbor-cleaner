package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// buildTestProject wires up a Project -> Repository -> Artifact tree with back-references,
// mirroring what the harbor adapter produces at runtime.
func buildTestProject(projectName, repoName string, artifacts ...*Artifact) *Project {
	project := &Project{Name: projectName}
	repo := &Repository{
		Project:           project,
		Name:              projectName + "/" + repoName,
		NameWithinProject: repoName,
		Artifacts:         artifacts,
	}
	for _, art := range artifacts {
		art.Repo = repo
	}
	project.Repos = []*Repository{repo}
	return project
}

func TestPreserveByMaxAge(t *testing.T) {
	young := &Artifact{Digest: "young", AgeHours: 10}
	old := &Artifact{Digest: "old", AgeHours: 1000}
	projects := []*Project{buildTestProject("proj", "repo", young, old)}

	preserved := PreserveByMaxAge(projects, 24*30) // 30 days
	assert.Equal(t, 1, preserved)
	assert.True(t, young.Preserve)
	assert.False(t, old.Preserve)
}

func TestPreserveByTopN(t *testing.T) {
	// AgePosition simulates having already run SortArtifactsByAgeAndCalculatePosition
	top1 := &Artifact{Digest: "top1", AgePosition: 1}
	top2 := &Artifact{Digest: "top2", AgePosition: 2}
	top3 := &Artifact{Digest: "top3", AgePosition: 3}
	projects := []*Project{buildTestProject("proj", "repo", top1, top2, top3)}

	preserved := PreserveByTopN(projects, 2)
	assert.Equal(t, 2, preserved)
	assert.True(t, top1.Preserve)
	assert.True(t, top2.Preserve)
	assert.False(t, top3.Preserve)
}

func TestPreserveByTopNWithFewerArtifactsThanN(t *testing.T) {
	only := &Artifact{Digest: "only", AgePosition: 1}
	projects := []*Project{buildTestProject("proj", "repo", only)}

	preserved := PreserveByTopN(projects, 5)
	assert.Equal(t, 1, preserved)
	assert.True(t, only.Preserve)
}

func TestPreserveByAllowListProjects(t *testing.T) {
	art := &Artifact{Digest: "art"}
	kept := &Artifact{Digest: "kept"}
	projects := []*Project{
		buildTestProject("clean-me", "repo", art),
		buildTestProject("keep-me", "repo", kept),
	}

	preserved := PreserveByAllowListProjects(projects, []string{"keep-me"})
	assert.Equal(t, 1, preserved)
	assert.False(t, art.Preserve)
	assert.True(t, kept.Preserve)
}

func TestPreserveByAllowListProjectsNoneSentinel(t *testing.T) {
	art := &Artifact{Digest: "art"}
	projects := []*Project{buildTestProject("any", "repo", art)}

	preserved := PreserveByAllowListProjects(projects, []string{"none"})
	assert.Equal(t, 0, preserved)
	assert.False(t, art.Preserve)
}

func TestPreserveByAllowListRepos(t *testing.T) {
	art := &Artifact{Digest: "art"}
	kept := &Artifact{Digest: "kept"}
	projects := []*Project{
		buildTestProject("proj", "clean-me", art),
		buildTestProject("proj", "keep-me", kept),
	}

	preserved := PreserveByAllowListRepos(projects, []string{"proj/keep-me"})
	assert.Equal(t, 1, preserved)
	assert.False(t, art.Preserve)
	assert.True(t, kept.Preserve)
}

func TestPreserveByDigestSet(t *testing.T) {
	live := &Artifact{Digest: "sha256:live"}
	dead := &Artifact{Digest: "sha256:dead"}
	projects := []*Project{buildTestProject("proj", "repo", live, dead)}

	preserved := PreserveByDigestSet(projects, map[string]struct{}{"sha256:live": {}})
	assert.Equal(t, 1, preserved)
	assert.True(t, live.Preserve)
	assert.False(t, dead.Preserve)
}

func TestPreserveRulesDoNotUnsetEachOther(t *testing.T) {
	art := &Artifact{Digest: "art", AgeHours: 1000, AgePosition: 5}
	projects := []*Project{buildTestProject("proj", "repo", art)}

	// сначала allow-list по проекту сохраняет артефакт...
	PreserveByAllowListProjects(projects, []string{"proj"})
	assert.True(t, art.Preserve)

	// ...потом должно быть 0 preserved у последующих правил, но флаг остаётся true
	preservedByAge := PreserveByMaxAge(projects, 1)
	assert.Equal(t, 0, preservedByAge)
	assert.True(t, art.Preserve)
}
