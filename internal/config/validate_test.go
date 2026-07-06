package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// validConfig returns a minimal CleanerConfig that passes Validate, so each
// test case only needs to override the one field it's exercising.
func validConfig() CleanerConfig {
	return CleanerConfig{
		PushedDaysAgo:          9,
		TopAge:                 1,
		WorkloadSource:         "none",
		DeleteMode:             "dry-run",
		SecretsProvider:        "vault",
		VaultAuthMode:          "jwt",
		HarborTimeoutMinutes:   7,
		VaultTimeoutMinutes:    1,
		K8sTimeoutMinutes:      5,
		CleaningTimeoutMinutes: 20,
	}
}

func TestValidateAcceptsMinimalValidConfig(t *testing.T) {
	cc := validConfig()
	assert.NoError(t, cc.Validate())
}

func TestValidatePushedDaysAgoTooLow(t *testing.T) {
	cc := validConfig()
	cc.PushedDaysAgo = 8
	assert.Error(t, cc.Validate())
}

func TestValidateTopAgeTooLow(t *testing.T) {
	cc := validConfig()
	cc.TopAge = 0
	assert.Error(t, cc.Validate())
}

func TestValidateUnknownWorkloadSource(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "bogus"
	assert.Error(t, cc.Validate())
}

func TestValidateUnknownDeleteMode(t *testing.T) {
	cc := validConfig()
	cc.DeleteMode = "bogus"
	assert.Error(t, cc.Validate())
}

func TestValidateSoftDeleteRejectsGarbageProjectInCleanList(t *testing.T) {
	cc := validConfig()
	cc.DeleteMode = "soft-delete"
	cc.GarbageProjectName = "trashcan"
	cc.ProjectsToClean = []string{"trashcan", "other"}
	assert.Error(t, cc.Validate())
}

func TestValidateSoftDeleteAcceptsCleanListWithoutGarbageProject(t *testing.T) {
	cc := validConfig()
	cc.DeleteMode = "soft-delete"
	cc.GarbageProjectName = "trashcan"
	cc.ProjectsToClean = []string{"other"}
	assert.NoError(t, cc.Validate())
}

func TestValidateUnknownSecretsProvider(t *testing.T) {
	cc := validConfig()
	cc.SecretsProvider = "bogus"
	assert.Error(t, cc.Validate())
}

func TestValidateUnknownVaultAuthMode(t *testing.T) {
	cc := validConfig()
	cc.VaultAuthMode = "bogus"
	assert.Error(t, cc.Validate())
}

func TestValidateK8sVaultAuthModeRequiresVaultSecretsProvider(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "vault"
	cc.SecretsProvider = "env"
	cc.Clusters = []string{"cluster-a"}
	assert.Error(t, cc.Validate())
}

func TestValidateK8sVaultAuthModeAcceptsVaultSecretsProvider(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "vault"
	cc.SecretsProvider = "vault"
	cc.Clusters = []string{"cluster-a"}
	assert.NoError(t, cc.Validate())
}

func TestValidateK8sLocalKubeconfigRequiresPath(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "local-kubeconfig"
	cc.K8sLocalKubeconfigPath = ""
	cc.Clusters = []string{"cluster-a"}
	assert.Error(t, cc.Validate())
}

func TestValidateK8sLocalKubeconfigAcceptsPath(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "local-kubeconfig"
	cc.K8sLocalKubeconfigPath = "/path/to/kubeconfig"
	cc.Clusters = []string{"cluster-a"}
	assert.NoError(t, cc.Validate())
}

func TestValidateK8sUnknownAuthMode(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "bogus"
	cc.Clusters = []string{"cluster-a"}
	assert.Error(t, cc.Validate())
}

func TestValidateK8sRequiresClustersUnlessInCluster(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "vault"
	cc.Clusters = nil
	assert.Error(t, cc.Validate())
}

func TestValidateK8sInClusterDoesNotRequireClusters(t *testing.T) {
	cc := validConfig()
	cc.WorkloadSource = "k8s"
	cc.K8sAuthMode = "in-cluster"
	cc.Clusters = nil
	assert.NoError(t, cc.Validate())
}
