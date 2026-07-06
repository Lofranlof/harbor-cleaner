package harbor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"harbor-cleaner/internal/domain"
	"harbor-cleaner/internal/ports"
	"harbor-cleaner/utils"

	log "github.com/sirupsen/logrus"

	hcl "github.com/goharbor/go-client/pkg/sdk/v2.0/client"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/artifact"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/project"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/repository"
	sdkmodels "github.com/goharbor/go-client/pkg/sdk/v2.0/models"
)

// Registry implements ports.ArtifactRegistry against a real Harbor instance.
type Registry struct {
	hcl *hcl.HarborAPI
	cfg Config
}

var _ ports.ArtifactRegistry = (*Registry)(nil)

func NewRegistry(client *hcl.HarborAPI, cfg Config) *Registry {
	return &Registry{hcl: client, cfg: cfg}
}

// ListAllProjects fetches every project, repository and artifact in the registry.
func (r *Registry) ListAllProjects(ctx context.Context) ([]*domain.Project, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	projs, err := r.listAllProjectsConc(ctxWithTimeout)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch projects from Harbor: %w", err)
	}

	repos, err := r.listAllRepositoriesConc(ctxWithTimeout)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch repositories from Harbor: %w", err)
	}

	artifacts, err := r.listAllArtifactsConc(ctxWithTimeout, repos)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch artifacts from Harbor: %w", err)
	}

	return assembleProjects(projs, repos, artifacts), nil
}

// ListProjects fetches only the named projects (and their repositories and artifacts).
func (r *Registry) ListProjects(ctx context.Context, names []string) ([]*domain.Project, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	var (
		projs     = make([]*sdkmodels.Project, 0, len(names))
		repos     = make([]*sdkmodels.Repository, 0, 100*len(names))
		artifacts = make([]*sdkmodels.Artifact, 0, 1000*len(names))
	)

	for _, name := range names {
		proj, err := r.getProjectByName(ctxWithTimeout, name)
		if err != nil {
			return nil, fmt.Errorf("couldn't fetch project %s from Harbor: %w", name, err)
		}

		projRepos, err := r.listRepositoriesInProjectConc(ctxWithTimeout, name)
		if err != nil {
			return nil, fmt.Errorf("couldn't fetch repositories of project %s from Harbor: %w", name, err)
		}

		projArtifacts, err := r.listArtifactsInProjectConc(ctxWithTimeout, name, projRepos)
		if err != nil {
			return nil, fmt.Errorf("couldn't fetch artifacts of project %s from Harbor: %w", name, err)
		}

		projs = append(projs, proj)
		repos = append(repos, projRepos...)
		artifacts = append(artifacts, projArtifacts...)
	}

	return assembleProjects(projs, repos, artifacts), nil
}

func (r *Registry) FakeDeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	artName := fmt.Sprintf("%s/%s@%s", projectName, repoName, digest)

	log.Tracef("Fake-deleting artifact: %s...", artName)
	_, err := r.hcl.Artifact.GetArtifact(ctxWithTimeout, &artifact.GetArtifactParams{
		ProjectName:    url.PathEscape(projectName),
		RepositoryName: url.PathEscape(repoName),
		Reference:      url.PathEscape(digest),
	})
	if err != nil {
		log.Warnf("Received error: %v when fake-deleting artifact: %s", err, artName)
		return err
	}
	return nil
}

func (r *Registry) DeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	artName := fmt.Sprintf("%s/%s@%s", projectName, repoName, digest)

	log.Tracef("Deleting artifact: %s...", artName)
	_, err := r.hcl.Artifact.DeleteArtifact(ctxWithTimeout, &artifact.DeleteArtifactParams{
		ProjectName:    url.PathEscape(projectName),
		RepositoryName: url.PathEscape(repoName),
		Reference:      url.PathEscape(digest),
	})
	if err != nil {
		log.Warnf("Received error: %v when deleting artifact: %s", err, artName)
		return err
	}
	return nil
}

// MoveArtifact copies an artifact into targetProjectName, prefixed with its
// original project name so its origin stays traceable, then deletes it from
// its source project.
func (r *Registry) MoveArtifact(ctx context.Context, sourceProjectName, targetProjectName, repoName, digest string) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	artName := fmt.Sprintf("%s/%s@%s", sourceProjectName, repoName, digest)

	log.Tracef("Copying artifact: %s...", artName)
	_, err := r.hcl.Artifact.CopyArtifact(ctxWithTimeout, &artifact.CopyArtifactParams{
		From:           fmt.Sprintf("%s/%s@%s", sourceProjectName, repoName, digest),
		ProjectName:    url.PathEscape(targetProjectName),
		RepositoryName: url.PathEscape(fmt.Sprintf("%s/%s", sourceProjectName, repoName)),
	})
	if err != nil {
		return fmt.Errorf("couldn't copy artifact %s: %w", artName, err)
	}

	log.Tracef("Deleting artifact: %s...", artName)
	_, err = r.hcl.Artifact.DeleteArtifact(ctxWithTimeout, &artifact.DeleteArtifactParams{
		ProjectName:    url.PathEscape(sourceProjectName),
		RepositoryName: url.PathEscape(repoName),
		Reference:      url.PathEscape(digest),
	})
	if err != nil {
		return fmt.Errorf("couldn't delete artifact %s after copying it: %w", artName, err)
	}
	return nil
}

// --- fetching helpers, ported from the pre-refactor internal/harbor.go ---

func (r *Registry) listAllProjectsConc(ctx context.Context) ([]*sdkmodels.Project, error) {
	fn := func(ctx context.Context, pageNum int64) (*project.ListProjectsOK, error) {
		return r.hcl.Project.ListProjects(ctx, &project.ListProjectsParams{PageSize: &r.cfg.PageSize, Page: &pageNum})
	}
	isDone := func(resp *project.ListProjectsOK) bool { return len(resp.Payload) == 0 }

	log.Info("Fetching projects from Harbor...")
	responses, errs := utils.FetchConcurrently(ctx, r.cfg.NumOfWorkersAllProjects, fn, isDone, r.cfg.Timeout)
	if len(errs) > 0 {
		return nil, fmt.Errorf("encountered errors while fetching projects: %v", errs)
	}
	projects := make([]*sdkmodels.Project, 0)
	for _, resp := range responses {
		projects = append(projects, resp.Payload...)
	}
	log.Infof("Fetched %d projects from Harbor", len(projects))
	return projects, nil
}

func (r *Registry) getProjectByName(ctx context.Context, name string) (*sdkmodels.Project, error) {
	projects, err := r.listAllProjectsConc(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("did not find project with name: %s in Harbor", name)
}

func (r *Registry) listAllRepositoriesConc(ctx context.Context) ([]*sdkmodels.Repository, error) {
	fn := func(ctx context.Context, pageNum int64) (*repository.ListAllRepositoriesOK, error) {
		return r.hcl.Repository.ListAllRepositories(ctx, &repository.ListAllRepositoriesParams{PageSize: &r.cfg.PageSize, Page: &pageNum})
	}
	isDone := func(resp *repository.ListAllRepositoriesOK) bool { return len(resp.Payload) == 0 }

	log.Info("Fetching all repositories from Harbor...")
	responses, errs := utils.FetchConcurrently(ctx, r.cfg.NumOfWorkersAllRepos, fn, isDone, r.cfg.Timeout)
	if len(errs) > 0 {
		return nil, fmt.Errorf("encountered errors while fetching repositories: %v", errs)
	}
	repos := make([]*sdkmodels.Repository, 0)
	for _, resp := range responses {
		repos = append(repos, resp.Payload...)
	}
	log.Infof("Fetched %d repositories from Harbor", len(repos))
	return repos, nil
}

func (r *Registry) listRepositoriesInProjectConc(ctx context.Context, projectName string) ([]*sdkmodels.Repository, error) {
	fn := func(ctx context.Context, pageNum int64) (*repository.ListRepositoriesOK, error) {
		return r.hcl.Repository.ListRepositories(ctx, &repository.ListRepositoriesParams{
			PageSize: &r.cfg.PageSize, Page: &pageNum, ProjectName: url.PathEscape(projectName),
		})
	}
	isDone := func(resp *repository.ListRepositoriesOK) bool { return len(resp.Payload) == 0 }

	log.Infof("Fetching repositories from project %s...", projectName)
	responses, errs := utils.FetchConcurrently(ctx, r.cfg.NumOfWorkersProjectRepos, fn, isDone, r.cfg.Timeout)
	if len(errs) > 0 {
		return nil, fmt.Errorf("encountered errors while fetching repositories: %v", errs)
	}
	repos := make([]*sdkmodels.Repository, 0)
	for _, resp := range responses {
		repos = append(repos, resp.Payload...)
	}
	return repos, nil
}

func (r *Registry) listArtifactsInRepoConc(ctx context.Context, projectName, repoNameWithinProject string) ([]*sdkmodels.Artifact, []error) {
	fn := func(ctx context.Context, pageNum int64) (*artifact.ListArtifactsOK, error) {
		return r.hcl.Artifact.ListArtifacts(ctx, &artifact.ListArtifactsParams{
			PageSize:       &r.cfg.PageSize,
			Page:           &pageNum,
			ProjectName:    url.PathEscape(projectName),
			RepositoryName: url.PathEscape(repoNameWithinProject),
		})
	}
	isDone := func(resp *artifact.ListArtifactsOK) bool { return len(resp.Payload) == 0 }

	responses, errs := utils.FetchConcurrently(ctx, r.cfg.NumOfWorkersRepoArtifacts, fn, isDone, r.cfg.Timeout)
	artifacts := make([]*sdkmodels.Artifact, 0)
	for _, resp := range responses {
		artifacts = append(artifacts, resp.Payload...)
	}
	return artifacts, errs
}

// listArtifactsAcrossRepos fans out one goroutine per repo (each of which itself
// paginates concurrently via listArtifactsInRepoConc) and collects every artifact.
func (r *Registry) listArtifactsAcrossRepos(ctx context.Context, repos []*sdkmodels.Repository, projectNameOf func(*sdkmodels.Repository) string) ([]*sdkmodels.Artifact, error) {
	fetch := func(ctx context.Context, repo *sdkmodels.Repository) ([]*sdkmodels.Artifact, error) {
		data, errs := r.listArtifactsInRepoConc(ctx, projectNameOf(repo), utils.GetRepoNameWithinProject(repo.Name))
		if len(errs) > 0 {
			return nil, fmt.Errorf("encountered errors while fetching artifacts: %w", errors.Join(errs...))
		}
		return data, nil
	}

	perRepo, err := utils.Gather(ctx, repos, fetch)
	if err != nil {
		return nil, err
	}

	artifacts := make([]*sdkmodels.Artifact, 0)
	for _, data := range perRepo {
		artifacts = append(artifacts, data...)
	}
	return artifacts, nil
}

func (r *Registry) listAllArtifactsConc(ctx context.Context, repos []*sdkmodels.Repository) ([]*sdkmodels.Artifact, error) {
	log.Info("Fetching artifacts from Harbor...")
	artifacts, err := r.listArtifactsAcrossRepos(ctx, repos, func(repo *sdkmodels.Repository) string {
		return utils.GetProjectNameOfRepository(repo.Name)
	})
	if err != nil {
		return nil, err
	}
	log.Infof("Fetched %d artifacts from Harbor", len(artifacts))
	return artifacts, nil
}

func (r *Registry) listArtifactsInProjectConc(ctx context.Context, projectName string, repos []*sdkmodels.Repository) ([]*sdkmodels.Artifact, error) {
	log.Infof("Fetching artifacts from project: %s", projectName)
	artifacts, err := r.listArtifactsAcrossRepos(ctx, repos, func(*sdkmodels.Repository) string { return projectName })
	if err != nil {
		return nil, err
	}
	log.Infof("Fetched %d artifacts from project %s", len(artifacts), projectName)
	return artifacts, nil
}

// --- assembly: turn Harbor's flat project/repository/artifact lists into a
// domain.Project tree. Harbor's numeric ProjectID/RepositoryID only make sense
// here - once assembled, the rest of the application only ever sees domain types.

func assembleProjects(projs []*sdkmodels.Project, repos []*sdkmodels.Repository, artifacts []*sdkmodels.Artifact) []*domain.Project {
	// Index repos by project and artifacts by repo up front, instead of
	// rescanning the full repos/artifacts slices for every project/repo -
	// at Harbor's documented scale (~3000 repos, ~65000 artifacts) a nested
	// scan means ~R*A comparisons per full sync, this is O(P+R+A).
	reposByProject := make(map[int64][]*sdkmodels.Repository, len(projs))
	for _, repo := range repos {
		reposByProject[repo.ProjectID] = append(reposByProject[repo.ProjectID], repo)
	}
	artifactsByRepo := make(map[int64][]*sdkmodels.Artifact, len(repos))
	for _, art := range artifacts {
		artifactsByRepo[art.RepositoryID] = append(artifactsByRepo[art.RepositoryID], art)
	}

	domainProjects := make([]*domain.Project, 0, len(projs))

	for _, proj := range projs {
		domainProject := &domain.Project{Name: proj.Name}

		projRepos := reposByProject[int64(proj.ProjectID)]
		domainRepos := make([]*domain.Repository, 0, len(projRepos))
		for _, repo := range projRepos {
			domainRepo := &domain.Repository{
				Project:           domainProject,
				Name:              repo.Name,
				NameWithinProject: utils.GetRepoNameWithinProject(repo.Name),
			}

			repoArtifacts := artifactsByRepo[repo.ID]
			domainArtifacts := make([]*domain.Artifact, 0, len(repoArtifacts))
			for _, art := range repoArtifacts {
				domainArt := domain.NewArtifact(art.Digest, art.Size, time.Time(art.PushTime), tagsOf(art))
				domainArt.Repo = domainRepo
				domainArtifacts = append(domainArtifacts, domainArt)
			}

			domainRepo.Artifacts = domainArtifacts
			domainRepos = append(domainRepos, domainRepo)
		}

		domainProject.Repos = domainRepos
		domainProjects = append(domainProjects, domainProject)
	}

	return domainProjects
}

func tagsOf(art *sdkmodels.Artifact) []domain.Tag {
	tags := make([]domain.Tag, 0, len(art.Tags))
	for _, t := range art.Tags {
		tags = append(tags, domain.Tag{Name: t.Name, PushTime: time.Time(t.PushTime)})
	}
	return tags
}
