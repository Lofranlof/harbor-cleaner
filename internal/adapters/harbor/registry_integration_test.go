//go:build integration

// This test spins up a real Harbor instance (via its own official installer,
// orchestrated through testcontainers-go) and exercises the harbor adapter's
// ArtifactRegistry implementation against it end to end - list, an actual
// image push, and delete - rather than against a hand-rolled fake HTTP
// server. It requires Docker and network access (to fetch the Harbor
// installer, its container images, and a small test image to push), and
// takes a few minutes since Harbor is a multi-container application.
//
// Run explicitly: go test -tags=integration ./internal/adapters/harbor/...
package harbor_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	harboradapter "harbor-cleaner/internal/adapters/harbor"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/compose"
)

const (
	harborVersion  = "v2.15.2"
	harborAdmin    = "admin"
	harborPassword = "Harbor12345"
)

// TestHarborRegistryAgainstRealHarbor downloads Harbor's official online
// installer, generates a docker-compose stack for a single-node, HTTP-only
// Harbor via Harbor's own `prepare` tool (so we never have to hand-maintain
// Harbor's internal service wiring), brings it up with testcontainers-go, and
// then drives internal/adapters/harbor.Registry against it exactly the way
// the real cleaner would.
func TestHarborRegistryAgainstRealHarbor(t *testing.T) {
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

	imageRef := pushTestImage(t, httpPort)

	registryURL, err := url.Parse(baseURL + "/api/v2.0")
	require.NoError(t, err)

	harborCfg := harboradapter.Config{
		URL:                       registryURL,
		PageSize:                  100,
		NumOfWorkersAllProjects:   1,
		NumOfWorkersAllRepos:      5,
		NumOfWorkersProjectRepos:  2,
		NumOfWorkersRepoArtifacts: 2,
		Timeout:                   2 * time.Minute,
	}
	client, err := harboradapter.NewClient(harborCfg, harborAdmin, harborPassword)
	require.NoError(t, err)
	registry := harboradapter.NewRegistry(client, harborCfg)

	projects, err := registry.ListProjects(ctx, []string{"library"})
	require.NoError(t, err)
	require.Len(t, projects, 1, "expected the seeded \"library\" project")
	require.Len(t, projects[0].Repos, 1, "expected the repo we just pushed: %s", imageRef)
	repo := projects[0].Repos[0]
	require.Len(t, repo.Artifacts, 1)
	digest := repo.Artifacts[0].Digest

	require.NoError(t, registry.FakeDeleteArtifact(ctx, "library", repo.NameWithinProject, digest))

	require.NoError(t, registry.DeleteArtifact(ctx, "library", repo.NameWithinProject, digest))

	projectsAfterDelete, err := registry.ListProjects(ctx, []string{"library"})
	require.NoError(t, err)
	// Harbor keeps the (now empty) repository entry around after its last
	// artifact is deleted - only the artifact itself is guaranteed gone.
	require.Len(t, projectsAfterDelete[0].Repos, 1)
	require.Empty(t, projectsAfterDelete[0].Repos[0].Artifacts, "artifact should be gone after DeleteArtifact")
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available, skipping Harbor integration test")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable, skipping Harbor integration test")
	}
}

func downloadAndExtractInstaller(t *testing.T, destDir string) {
	t.Helper()
	url := fmt.Sprintf("https://github.com/goharbor/harbor/releases/download/%s/harbor-online-installer-%s.tgz", harborVersion, harborVersion)

	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "downloading Harbor installer from %s", url)

	gz, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		// the tarball has a top-level "harbor/" directory - strip it so
		// destDir directly contains harbor.yml.tmpl, prepare, etc.
		name := strings.TrimPrefix(hdr.Name, "harbor/")
		if name == "" {
			continue
		}
		target := filepath.Join(destDir, name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			require.NoError(t, os.MkdirAll(target, 0755))
		case tar.TypeReg:
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			require.NoError(t, err)
			_, err = io.Copy(f, tr)
			f.Close()
			require.NoError(t, err)
		}
	}
}

func writeHarborYML(t *testing.T, installerDir string, httpPort int) {
	t.Helper()
	dataDir := t.TempDir()
	content := fmt.Sprintf(`hostname: localhost
http:
  port: %d
harbor_admin_password: %s
database:
  password: root123
  max_idle_conns: 20
  max_open_conns: 50
  conn_max_lifetime: 5m
data_volume: %s
trivy:
  ignore_unfixed: false
  skip_update: true
  skip_java_db_update: true
  offline_scan: true
  security_check: vuln
  insecure: false
jobservice:
  max_job_workers: 5
  job_loggers:
    - STD_OUTPUT
  logger_sweeper_duration: 1
notification:
  webhook_job_max_retry: 3
  webhook_job_http_client_timeout: 3
log:
  level: info
  local:
    rotate_count: 50
    rotate_size: 200M
    location: /var/log/harbor
_version: 2.15.0
proxy:
  components:
    - core
    - jobservice
    - trivy
upload_purging:
  enabled: true
  age: 168h
  interval: 24h
  dryrun: false
cache:
  enabled: false
  expire_hours: 24
`, httpPort, harborPassword, dataDir)

	require.NoError(t, os.WriteFile(filepath.Join(installerDir, "harbor.yml"), []byte(content), 0644))
}

// runPrepare shells out to Harbor's own `prepare` script, which itself runs
// `docker run goharbor/prepare:<version>` to render docker-compose.yml and
// every service's config/certs from harbor.yml. Delegating to Harbor's own
// tooling here avoids having to hand-maintain Harbor's internal service
// wiring as a second, drifting copy.
func runPrepare(t *testing.T, installerDir string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(installerDir, "prepare"))
	cmd.Dir = installerDir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "harbor prepare failed:\n%s", string(out))
}

func waitForPing(t *testing.T, baseURL string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/v2.0/ping")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("Harbor did not become ready within %s: %v", timeout, lastErr)
}

// pushTestImage pulls a tiny public image and pushes it into Harbor's
// "library" project, so the registry has a real artifact to list and delete.
func pushTestImage(t *testing.T, httpPort int) string {
	t.Helper()
	registryHost := fmt.Sprintf("localhost:%d", httpPort)
	imageRef := fmt.Sprintf("%s/library/harbor-cleaner-integration-test:latest", registryHost)

	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "%s %v failed:\n%s", name, args, string(out))
	}

	run("docker", "login", registryHost, "-u", harborAdmin, "-p", harborPassword)
	run("docker", "pull", "alpine:latest")
	run("docker", "tag", "alpine:latest", imageRef)
	run("docker", "push", imageRef)
	return imageRef
}
