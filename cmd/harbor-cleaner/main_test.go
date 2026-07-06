package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"harbor-cleaner/internal/app"
	"harbor-cleaner/internal/config"
	"harbor-cleaner/internal/domain"
	"harbor-cleaner/internal/ports"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalKubeconfig is just enough for client-go to parse into a rest.Config
// without contacting any server.
const minimalKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test-cluster
  cluster:
    server: https://example.invalid:6443
contexts:
- name: test-context
  context:
    cluster: test-cluster
    user: test-user
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`

// fakeSecretsProvider is an in-memory ports.SecretsProvider for exercising
// buildK8sClientsets without a real Vault.
type fakeSecretsProvider struct {
	kubeconfigsByCluster map[string]string
}

func (f *fakeSecretsProvider) HarborCredentials(ctx context.Context) (ports.HarborCredentials, error) {
	return ports.HarborCredentials{}, nil
}

func (f *fakeSecretsProvider) Kubeconfigs(ctx context.Context, clusterNames []string) (map[string]string, error) {
	return f.kubeconfigsByCluster, nil
}

func TestBuildSecretsProviderUnknown(t *testing.T) {
	_, err := buildSecretsProvider(&config.CleanerConfig{SecretsProvider: "bogus"})
	assert.Error(t, err)
}

func TestBuildSecretsProviderEnv(t *testing.T) {
	t.Setenv("TEST_LOGIN", "admin")
	t.Setenv("TEST_PASSWORD", "hunter2")

	provider, err := buildSecretsProvider(&config.CleanerConfig{
		SecretsProvider:               "env",
		VaultHarborLoginSecretName:    "TEST_LOGIN",
		VaultHarborPasswordSecretName: "TEST_PASSWORD",
	})
	require.NoError(t, err)

	creds, err := provider.HarborCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "admin", creds.Login)
	assert.Equal(t, "hunter2", creds.Password)
}

func TestBuildVaultClientUnknownAuthMode(t *testing.T) {
	_, err := buildVaultClient(&config.CleanerConfig{VaultAuthMode: "bogus"}, 0)
	assert.Error(t, err)
}

func TestBuildWorkloadSourceNone(t *testing.T) {
	source, err := buildWorkloadSource(context.Background(), &config.CleanerConfig{WorkloadSource: "none"}, nil)
	require.NoError(t, err)

	refs, err := source.LiveImageRefs(context.Background())
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func TestBuildWorkloadSourceUnknown(t *testing.T) {
	_, err := buildWorkloadSource(context.Background(), &config.CleanerConfig{WorkloadSource: "bogus"}, nil)
	assert.Error(t, err)
}

func TestBuildK8sClientsetsUnknownAuthMode(t *testing.T) {
	_, err := buildK8sClientsets(context.Background(), &config.CleanerConfig{K8sAuthMode: "bogus"}, nil)
	assert.Error(t, err)
}

func TestBuildK8sClientsetsLocalKubeconfigMissingFile(t *testing.T) {
	cfg := &config.CleanerConfig{
		K8sAuthMode:            "local-kubeconfig",
		K8sLocalKubeconfigPath: filepath.Join(t.TempDir(), "does-not-exist"),
	}
	_, err := buildK8sClientsets(context.Background(), cfg, nil)
	assert.Error(t, err)
}

func TestBuildK8sClientsetsVaultAuthModeBuildsOneClientPerCluster(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(minimalKubeconfig))
	secretsProvider := &fakeSecretsProvider{
		kubeconfigsByCluster: map[string]string{"cluster-a": encoded, "cluster-b": encoded},
	}
	cfg := &config.CleanerConfig{
		K8sAuthMode: "vault",
		Clusters:    []string{"cluster-a", "cluster-b"},
	}

	clientsets, err := buildK8sClientsets(context.Background(), cfg, secretsProvider)

	require.NoError(t, err)
	assert.Len(t, clientsets, 2)
}

func TestConfigureLoggerSetsLevelForStdout(t *testing.T) {
	defer log.SetOutput(os.Stderr)

	configureLogger(&config.CleanerConfig{LogLevel: "warn", LogTarget: "stdout"})

	assert.Equal(t, log.WarnLevel, log.GetLevel())
}

func TestConfigureLoggerWritesToFile(t *testing.T) {
	defer log.SetOutput(os.Stderr)
	path := filepath.Join(t.TempDir(), "harbor-cleaner.log")

	configureLogger(&config.CleanerConfig{LogLevel: "info", LogTarget: "file", LogPath: path})
	log.Info("hello from test")

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from test")
}

// fakeRegistry is a minimal in-memory ports.ArtifactRegistry, just enough to
// drive a real app.Cleaner through Collect for logCleaningPlan's sake.
type fakeRegistry struct {
	projects []*domain.Project
}

func (f *fakeRegistry) ListAllProjects(ctx context.Context) ([]*domain.Project, error) {
	return f.projects, nil
}
func (f *fakeRegistry) ListProjects(ctx context.Context, names []string) ([]*domain.Project, error) {
	return f.projects, nil
}
func (f *fakeRegistry) FakeDeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	return nil
}
func (f *fakeRegistry) DeleteArtifact(ctx context.Context, projectName, repoName, digest string) error {
	return nil
}
func (f *fakeRegistry) MoveArtifact(ctx context.Context, sourceProjectName, targetProjectName, repoName, digest string) error {
	return nil
}

func TestLogCleaningPlanLogsUnpreservedArtifactsOnly(t *testing.T) {
	toClean := &domain.Artifact{Digest: "sha256:clean-me", SizeBytes: 5 * 1024 * 1024 * 1024}
	preserved := &domain.Artifact{Digest: "sha256:keep-me", SizeBytes: 9 * 1024 * 1024 * 1024, Preserve: true}
	repo := &domain.Repository{Name: "proj/repo", NameWithinProject: "repo", Artifacts: []*domain.Artifact{toClean, preserved}}
	project := &domain.Project{Name: "proj", Repos: []*domain.Repository{repo}}
	repo.Project = project
	toClean.Repo = repo
	preserved.Repo = repo

	registry := &fakeRegistry{projects: []*domain.Project{project}}
	cleaner := app.NewCleaner(app.Options{ProjectsToClean: []string{"all"}}, registry, nil)
	require.NoError(t, cleaner.Collect(context.Background()))

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	logCleaningPlan("registry.example.com", cleaner)

	output := buf.String()
	assert.Contains(t, output, "sha256:clean-me")
	assert.NotContains(t, output, "sha256:keep-me")
	assert.Contains(t, output, "5 GB")
}
