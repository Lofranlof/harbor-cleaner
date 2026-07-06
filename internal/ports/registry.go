package ports

import (
	"context"

	"harbor-cleaner/internal/domain"
)

// ArtifactRegistry is the read/write boundary onto a container registry. The
// concrete implementation (see internal/adapters/harbor) talks to Harbor; the
// domain and application layers only ever see this interface, so business rules
// can be tested against a fake without a real registry.
//
// ListAllProjects/ListProjects return fully assembled domain.Project trees
// (Repos and Artifacts populated, back-references wired up) - reassembling a
// tree out of Harbor's flat project/repository/artifact lists requires
// Harbor-internal numeric IDs that have no meaning outside the adapter, so that
// assembly stays inside it rather than leaking into the domain model.
type ArtifactRegistry interface {
	// ListAllProjects fetches every project, repository and artifact in the
	// registry.
	ListAllProjects(ctx context.Context) ([]*domain.Project, error)
	// ListProjects fetches only the named projects (and their repositories and
	// artifacts). Used when the operator wants to clean specific projects
	// instead of scanning the whole registry.
	ListProjects(ctx context.Context, names []string) ([]*domain.Project, error)

	// FakeDeleteArtifact performs a read-only request in place of a real delete,
	// for dry-run mode. repoName is the repository name *within* its project
	// (i.e. domain.Repository.NameWithinProject, without the project prefix).
	FakeDeleteArtifact(ctx context.Context, projectName, repoName, digest string) error
	// DeleteArtifact permanently removes an artifact. repoName is the repository
	// name within its project (no project prefix).
	DeleteArtifact(ctx context.Context, projectName, repoName, digest string) error
	// MoveArtifact copies an artifact into targetProjectName and deletes it from
	// its original project - used for soft-delete ("move to trash"). repoName is
	// the repository name within sourceProjectName (no project prefix).
	MoveArtifact(ctx context.Context, sourceProjectName, targetProjectName, repoName, digest string) error
}
