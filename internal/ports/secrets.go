package ports

import "context"

// HarborCredentials is a login/password pair for authenticating against Harbor.
type HarborCredentials struct {
	Login    string
	Password string
}

// SecretsProvider resolves the credentials the cleaner needs at startup. The
// two implementations (see internal/adapters/vault and internal/adapters/envsecrets)
// are equally valid ways to run the tool - Vault is not a hard requirement.
type SecretsProvider interface {
	// HarborCredentials returns the login/password used to authenticate against Harbor.
	HarborCredentials(ctx context.Context) (HarborCredentials, error)
	// Kubeconfigs returns a base64-encoded kubeconfig for each requested cluster
	// name, keyed by cluster name. Only called when the configured WorkloadSource
	// needs cluster access (see ports.WorkloadSource / adapters/k8s).
	Kubeconfigs(ctx context.Context, clusterNames []string) (map[string]string, error)
}
