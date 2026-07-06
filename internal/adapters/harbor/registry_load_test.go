//go:build integration && load

// This is a separate, heavier opt-in test on top of registry_integration_test.go:
// it pushes a large batch of distinct synthetic artifacts straight over the
// registry HTTP API (via go-containerregistry, not `docker build`/`docker push` -
// those would take far too long per image for a batch this size) and then
// drives internal/app.Cleaner's real hard-delete path against all of them, the
// same way the composition root in cmd/harbor-cleaner does. It exists to catch
// what a handful of artifacts can't: worker-pool starvation, goroutine leaks,
// or pagination bugs in FetchConcurrently that only show up at volume.
//
// Requires both build tags because it reuses registry_integration_test.go's
// Harbor bring-up helpers (requireDocker, downloadAndExtractInstaller, etc.),
// which only compile under "integration".
//
// Run explicitly: go test -tags="integration load" ./internal/adapters/harbor/... -run TestHarborRegistryBulkHardDelete -timeout 15m
package harbor_test

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	harboradapter "harbor-cleaner/internal/adapters/harbor"
	"harbor-cleaner/internal/app"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/compose"
)

const (
	bulkArtifactCount       = 1000
	bulkArtifactRepoName    = "harbor-cleaner-load-test"
	bulkPushConcurrency     = 40
	bulkCleaningConcurrency = 40
)

// TestHarborRegistryBulkHardDelete pushes bulkArtifactCount distinct artifacts
// into a fresh Harbor instance and hard-deletes all of them through
// app.Cleaner, verifying every one is actually gone afterwards.
func TestHarborRegistryBulkHardDelete(t *testing.T) {
	requireDocker(t)

	installerDir := t.TempDir()
	downloadAndExtractInstaller(t, installerDir)

	httpPort := freeTCPPort(t)
	writeHarborYML(t, installerDir, httpPort)
	runPrepare(t, installerDir)

	ctx := context.Background()
	composeFilePath := filepath.Join(installerDir, "docker-compose.yml")
	stack, err := compose.NewDockerCompose(composeFilePath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = stack.Down(context.Background(), compose.RemoveOrphans(true), compose.RemoveVolumes(true))
	})
	require.NoError(t, stack.Up(ctx))

	baseURL := fmt.Sprintf("http://localhost:%d", httpPort)
	waitForPing(t, baseURL, 3*time.Minute)

	pushStart := time.Now()
	pushSyntheticArtifacts(t, httpPort, bulkArtifactRepoName, bulkArtifactCount, bulkPushConcurrency)
	t.Logf("pushed %d synthetic artifacts in %s", bulkArtifactCount, time.Since(pushStart))

	registryURL, err := url.Parse(baseURL + "/api/v2.0")
	require.NoError(t, err)
	harborCfg := harboradapter.Config{
		URL:                       registryURL,
		PageSize:                  100,
		NumOfWorkersAllProjects:   1,
		NumOfWorkersAllRepos:      5,
		NumOfWorkersProjectRepos:  5,
		NumOfWorkersRepoArtifacts: 10,
		Timeout:                   3 * time.Minute,
	}
	client, err := harboradapter.NewClient(harborCfg, harborAdmin, harborPassword)
	require.NoError(t, err)
	registry := harboradapter.NewRegistry(client, harborCfg)

	projects, err := registry.ListProjects(ctx, []string{"library"})
	require.NoError(t, err)
	require.Len(t, projects, 1)
	var repoArtifactCount int
	for _, r := range projects[0].Repos {
		if r.NameWithinProject == bulkArtifactRepoName {
			repoArtifactCount = len(r.Artifacts)
		}
	}
	require.Equal(t, bulkArtifactCount, repoArtifactCount, "expected every pushed artifact to be visible before cleaning")

	cleaner := app.NewCleaner(app.Options{
		ProjectsToClean:      []string{"library"},
		ProjectsToPreserve:   []string{"none"},
		ReposToPreserve:      []string{"none"},
		DeleteMode:           "hard-delete",
		RegistryHost:         fmt.Sprintf("localhost:%d", httpPort),
		NumOfWorkersCleaning: bulkCleaningConcurrency,
	}, registry, nil)

	require.NoError(t, cleaner.Collect(ctx))
	// Deliberately not calling cleaner.Preserve: PreserveByMaxAge would keep
	// every artifact here, since they were all just pushed seconds ago - that
	// rule is exercised elsewhere (internal/domain/preserve_test.go). This
	// test's job is bulk hard-delete, so every artifact stays eligible
	// (domain.Artifact.Preserve defaults to false).

	cleanCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cleanStart := time.Now()
	err = cleaner.Clean(cleanCtx)
	t.Logf("cleaned %d artifacts in %s", bulkArtifactCount, time.Since(cleanStart))
	require.NoError(t, err)

	projectsAfterClean, err := registry.ListProjects(ctx, []string{"library"})
	require.NoError(t, err)
	for _, r := range projectsAfterClean[0].Repos {
		if r.NameWithinProject == bulkArtifactRepoName {
			require.Empty(t, r.Artifacts, "every pushed artifact should be hard-deleted")
		}
	}
}

// pushSyntheticArtifacts pushes n distinct randomly-generated single-layer
// images into repoName, bounded to concurrency in-flight pushes at a time.
// Each is a genuinely distinct artifact (distinct digest), not just a new tag
// on the same content, so this exercises real pagination/volume, not just tag
// fan-out.
func pushSyntheticArtifacts(t *testing.T, httpPort int, repoName string, n, concurrency int) {
	t.Helper()
	registryHost := fmt.Sprintf("localhost:%d", httpPort)
	auth := authn.FromConfig(authn.AuthConfig{Username: harborAdmin, Password: harborPassword})

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	errsCh := make(chan error, n)

	for i := 0; i < n; i++ {
		i := i
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			img, err := random.Image(512, 1)
			if err != nil {
				errsCh <- fmt.Errorf("generating image %d: %w", i, err)
				return
			}
			ref, err := name.ParseReference(fmt.Sprintf("%s/library/%s:tag-%d", registryHost, repoName, i))
			if err != nil {
				errsCh <- fmt.Errorf("parsing reference %d: %w", i, err)
				return
			}
			if err := remote.Write(ref, img, remote.WithAuth(auth)); err != nil {
				errsCh <- fmt.Errorf("pushing image %d: %w", i, err)
				return
			}
		}()
	}
	wg.Wait()
	close(errsCh)

	for err := range errsCh {
		require.NoError(t, err)
	}
}
