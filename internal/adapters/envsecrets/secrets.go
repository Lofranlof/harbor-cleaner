// Package envsecrets implements ports.SecretsProvider by reading plain
// environment variables. It is a first-class alternative to Vault, not a test
// double - useful for local development, CI without Vault access, or any
// deployment that manages secrets some other way (e.g. a K8s Secret mounted as
// env vars). It has no way to resolve kubeconfigs; pair it with a WorkloadSource
// that doesn't need one (see internal/adapters/noworkload), or with a
// adapters/k8s client built from a local kubeconfig file / in-cluster config.
package envsecrets

import (
	"context"
	"fmt"
	"os"

	"harbor-cleaner/internal/ports"
)

// Config names the environment variables holding the Harbor credentials.
type Config struct {
	HarborLoginEnvVar    string
	HarborPasswordEnvVar string
}

type SecretsProvider struct {
	cfg Config
}

var _ ports.SecretsProvider = (*SecretsProvider)(nil)

func NewSecretsProvider(cfg Config) *SecretsProvider {
	return &SecretsProvider{cfg: cfg}
}

func (p *SecretsProvider) HarborCredentials(ctx context.Context) (ports.HarborCredentials, error) {
	login, ok := os.LookupEnv(p.cfg.HarborLoginEnvVar)
	if !ok {
		return ports.HarborCredentials{}, fmt.Errorf("environment variable %s is not set", p.cfg.HarborLoginEnvVar)
	}
	password, ok := os.LookupEnv(p.cfg.HarborPasswordEnvVar)
	if !ok {
		return ports.HarborCredentials{}, fmt.Errorf("environment variable %s is not set", p.cfg.HarborPasswordEnvVar)
	}
	return ports.HarborCredentials{Login: login, Password: password}, nil
}

// Kubeconfigs always fails: this provider has no notion of Vault-stored
// kubeconfigs. Use the k8s adapter's local-kubeconfig or in-cluster
// constructors instead of relying on SecretsProvider for cluster access.
func (p *SecretsProvider) Kubeconfigs(ctx context.Context, clusterNames []string) (map[string]string, error) {
	return nil, fmt.Errorf("envsecrets provider cannot resolve kubeconfigs; use a local kubeconfig file or in-cluster config instead")
}
