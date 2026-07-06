// Package app wires the domain's retention rules to the outside world via
// ports.ArtifactRegistry/ports.WorkloadSource. It is the only layer, besides
// the composition root in cmd/, that is allowed to depend on both the domain
// and the ports packages - domain itself must stay dependency-free, and ports
// only declares interfaces.
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"harbor-cleaner/internal/domain"
	"harbor-cleaner/internal/ports"

	"github.com/avast/retry-go/v4"
	log "github.com/sirupsen/logrus"
)

// Options carries every retention/cleaning knob the Cleaner needs. It is
// deliberately independent from internal/config.CleanerConfig so this package
// can be unit-tested without constructing a full application config.
type Options struct {
	// ProjectsToClean lists which projects to scan; ["all"] scans the whole registry.
	ProjectsToClean []string
	// ProjectsToPreserve/ReposToPreserve are allow-lists; ["none"] disables the rule.
	ProjectsToPreserve []string
	ReposToPreserve    []string
	// PushedDaysAgo is the age (in days) below which an artifact is always kept.
	PushedDaysAgo int
	// TopAge is how many of the freshest artifacts per repository are always kept.
	TopAge int
	// DeleteMode is one of "dry-run", "soft-delete", "hard-delete".
	DeleteMode string
	// GarbageProjectName is where soft-deleted artifacts get moved to.
	GarbageProjectName string
	// RegistryHost is used to build "registry/project/repo:tag" refs when
	// matching against a WorkloadSource's live image refs.
	RegistryHost string
	// NumOfWorkersCleaning bounds how many concurrent delete/move/get requests
	// are in flight against the registry.
	NumOfWorkersCleaning int
}

// Cleaner orchestrates the collect -> preserve -> clean pipeline. It knows
// nothing about Harbor, Vault or Kubernetes - only about the ports.ArtifactRegistry
// and ports.WorkloadSource interfaces it's constructed with.
type Cleaner struct {
	opts     Options
	registry ports.ArtifactRegistry
	// workload may be nil, meaning no live-workload preservation is performed
	// (retention then relies solely on age/top-N/allow-list rules).
	workload ports.WorkloadSource

	projects       []*domain.Project
	tagToDigestMap map[string]string
}

func NewCleaner(opts Options, registry ports.ArtifactRegistry, workload ports.WorkloadSource) *Cleaner {
	return &Cleaner{opts: opts, registry: registry, workload: workload}
}

// Projects returns the tree collected by Collect, after Preserve has run.
// Every artifact's Preserve flag reflects the outcome of the retention rules.
func (c *Cleaner) Projects() []*domain.Project { return c.projects }

// Collect fetches the project/repo/artifact tree to work on and computes each
// artifact's age ranking within its repository.
func (c *Cleaner) Collect(ctx context.Context) error {
	var (
		projects []*domain.Project
		err      error
	)
	if slices.Contains(c.opts.ProjectsToClean, "all") {
		projects, err = c.registry.ListAllProjects(ctx)
	} else {
		projects, err = c.registry.ListProjects(ctx, c.opts.ProjectsToClean)
	}
	if err != nil {
		return fmt.Errorf("couldn't collect projects from registry: %w", err)
	}

	for _, project := range projects {
		for _, repo := range project.Repos {
			repo.SortArtifactsByAgeAndCalculatePosition()
		}
	}

	c.projects = projects
	c.tagToDigestMap = buildTagToDigestMap(c.opts.RegistryHost, projects)
	return nil
}

// buildTagToDigestMap maps "registry/project/repo:tag" -> digest, so a
// WorkloadSource's image refs (which only ever name a tag, never a digest) can
// be resolved back to the artifact they point at.
func buildTagToDigestMap(registryHost string, projects []*domain.Project) map[string]string {
	m := make(map[string]string)
	for _, project := range projects {
		for _, repo := range project.Repos {
			for _, art := range repo.Artifacts {
				for _, tag := range art.Tags {
					ref := fmt.Sprintf("%s/%s/%s:%s", registryHost, project.Name, repo.NameWithinProject, tag.Name)
					m[ref] = art.Digest
				}
			}
		}
	}
	return m
}

// Preserve applies retention rules in sequence: images currently in use by a
// workload, explicitly allow-listed projects/repos, recently pushed images,
// and the top-N freshest images per repository.
func (c *Cleaner) Preserve(ctx context.Context) error {
	if c.workload != nil {
		refs, err := c.workload.LiveImageRefs(ctx)
		if err != nil {
			return fmt.Errorf("couldn't get live image refs from workload source: %w", err)
		}
		liveDigests := make(map[string]struct{}, len(refs))
		for ref := range refs {
			if digest, ok := c.tagToDigestMap[ref]; ok {
				liveDigests[digest] = struct{}{}
			}
		}
		preserved := domain.PreserveByDigestSet(c.projects, liveDigests)
		log.Infof("Preserved %d artifacts currently in use by a workload", preserved)
	}

	preserved := domain.PreserveByAllowListProjects(c.projects, c.opts.ProjectsToPreserve)
	log.Infof("Preserved %d artifacts from allow-listed projects: %v", preserved, c.opts.ProjectsToPreserve)

	preserved = domain.PreserveByAllowListRepos(c.projects, c.opts.ReposToPreserve)
	log.Infof("Preserved %d artifacts from allow-listed repos: %v", preserved, c.opts.ReposToPreserve)

	preserved = domain.PreserveByMaxAge(c.projects, c.opts.PushedDaysAgo*24)
	log.Infof("Preserved %d artifacts pushed less than %d days ago", preserved, c.opts.PushedDaysAgo)

	preserved = domain.PreserveByTopN(c.projects, c.opts.TopAge)
	log.Infof("Preserved %d artifacts in the top %d freshest per repo", preserved, c.opts.TopAge)

	return nil
}

// Clean deletes (or, in dry-run mode, just GETs) every non-preserved artifact,
// bounded by a fixed-size worker pool and retried on transient errors. Every
// artifact is attempted independently - one failing to delete does not stop
// the others - and every failure is collected, not just the first.
func (c *Cleaner) Clean(ctx context.Context) error {
	deleteFunc, err := c.deleteFuncFor(c.opts.DeleteMode)
	if err != nil {
		return err
	}

	toDelete := make([]*domain.Artifact, 0)
	for _, project := range c.projects {
		for _, repo := range project.Repos {
			for _, art := range repo.Artifacts {
				if !art.Preserve {
					toDelete = append(toDelete, art)
				}
			}
		}
	}
	log.Infof("Scheduling cleaning of %d artifacts...", len(toDelete))

	artifactsCh := make(chan *domain.Artifact)
	go func() {
		defer close(artifactsCh)
		for _, art := range toDelete {
			select {
			case artifactsCh <- art:
			case <-ctx.Done():
				return
			}
		}
	}()

	errsCh := make(chan error)
	var wg sync.WaitGroup
	wg.Add(c.opts.NumOfWorkersCleaning)
	for i := 0; i < c.opts.NumOfWorkersCleaning; i++ {
		go func() {
			defer wg.Done()
			for art := range artifactsCh {
				if cleanErr := c.cleanOne(ctx, deleteFunc, art); cleanErr != nil {
					select {
					case errsCh <- cleanErr:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(errsCh)
	}()

	var errs []error
	for cleanErr := range errsCh {
		errs = append(errs, cleanErr)
	}

	if len(errs) > 0 {
		return fmt.Errorf("couldn't clean %d artifact(s): %w", len(errs), errors.Join(errs...))
	}
	return nil
}

func (c *Cleaner) cleanOne(ctx context.Context, deleteFunc func(context.Context, *domain.Artifact) error, art *domain.Artifact) error {
	fullName := fmt.Sprintf("%s/%s@%s", c.opts.RegistryHost, art.Repo.Name, art.Digest)
	err := retry.Do(
		func() error { return deleteFunc(ctx, art) },
		retry.RetryIf(func(err error) bool {
			// Харбор мог отдать 504, при этом успев удалить артефакт - тогда
			// последующие попытки будут получать 404/400, ретраить их бессмысленно.
			return !isAlreadyResolved(err)
		}),
		retry.Attempts(5),
		retry.DelayType(retry.RandomDelay),
		retry.MaxJitter(5500*time.Millisecond),
		retry.OnRetry(func(n uint, err error) {
			log.Warnf("Retrying cleaning artifact %s after error: %v", fullName, err)
		}),
	)
	if err == nil {
		log.Debugf("Cleaned artifact: %s", fullName)
		return nil
	}
	if isAlreadyResolved(err) {
		log.Warnf("Received error: %v when cleaning artifact: %s, continuing", err, fullName)
		return nil
	}
	log.Errorf("Couldn't clean artifact %s due to error: %v", fullName, err)
	return err
}

// isAlreadyResolved reports whether err is an HTTP 404 or 400 response.
// Checked via IsCode, which every per-endpoint error type the Harbor SDK
// generates implements - not by matching the error text, which breaks
// silently if the SDK's message format ever changes.
func isAlreadyResolved(err error) bool {
	var sc interface{ IsCode(code int) bool }
	return errors.As(err, &sc) && (sc.IsCode(404) || sc.IsCode(400))
}

func (c *Cleaner) deleteFuncFor(mode string) (func(ctx context.Context, art *domain.Artifact) error, error) {
	switch mode {
	case "dry-run":
		return func(ctx context.Context, art *domain.Artifact) error {
			return c.registry.FakeDeleteArtifact(ctx, art.Repo.Project.Name, art.Repo.NameWithinProject, art.Digest)
		}, nil
	case "soft-delete":
		return func(ctx context.Context, art *domain.Artifact) error {
			return c.registry.MoveArtifact(ctx, art.Repo.Project.Name, c.opts.GarbageProjectName, art.Repo.NameWithinProject, art.Digest)
		}, nil
	case "hard-delete":
		return func(ctx context.Context, art *domain.Artifact) error {
			return c.registry.DeleteArtifact(ctx, art.Repo.Project.Name, art.Repo.NameWithinProject, art.Digest)
		}, nil
	default:
		return nil, fmt.Errorf("unknown delete-mode: %s", mode)
	}
}
