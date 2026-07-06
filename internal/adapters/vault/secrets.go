package vault

import (
	"context"
	"fmt"
	"sync"
	"time"

	"harbor-cleaner/internal/ports"
	"harbor-cleaner/utils"

	vaultapi "github.com/hashicorp/vault/api"
)

// Config carries the Vault paths/keys needed to resolve Harbor credentials and
// cluster kubeconfigs. Paths are "<engine>/<path>" as Vault CLI/UI display them;
// they get split into engine + path-within-engine internally.
type Config struct {
	Timeout time.Duration

	HarborCredsPath   string // e.g. "secret/myorg/harbor-cleaner"
	HarborLoginKey    string // key within the secret holding the Harbor username
	HarborPasswordKey string // key within the secret holding the Harbor password

	KubeconfigsPath string // e.g. "secret/kubeconfigs" - one sub-path per cluster name
	KubeconfigKey   string // key within each cluster's secret holding the kubeconfig
}

// SecretsProvider implements ports.SecretsProvider against Vault KV v2.
type SecretsProvider struct {
	client *vaultapi.Client
	cfg    Config
}

var _ ports.SecretsProvider = (*SecretsProvider)(nil)

func NewSecretsProvider(client *vaultapi.Client, cfg Config) *SecretsProvider {
	return &SecretsProvider{client: client, cfg: cfg}
}

func (p *SecretsProvider) HarborCredentials(ctx context.Context) (ports.HarborCredentials, error) {
	engine, path := utils.ParseVaultPath(p.cfg.HarborCredsPath)
	secret, err := readSecret(ctx, p.client, p.cfg.Timeout, engine, path)
	if err != nil {
		return ports.HarborCredentials{}, fmt.Errorf("couldn't read Harbor credentials from Vault at %s: %w", p.cfg.HarborCredsPath, err)
	}

	login, ok := secret.Data[p.cfg.HarborLoginKey].(string)
	if !ok {
		return ports.HarborCredentials{}, fmt.Errorf("secret at %s has no string field %q", p.cfg.HarborCredsPath, p.cfg.HarborLoginKey)
	}
	password, ok := secret.Data[p.cfg.HarborPasswordKey].(string)
	if !ok {
		return ports.HarborCredentials{}, fmt.Errorf("secret at %s has no string field %q", p.cfg.HarborCredsPath, p.cfg.HarborPasswordKey)
	}

	return ports.HarborCredentials{Login: login, Password: password}, nil
}

func (p *SecretsProvider) Kubeconfigs(ctx context.Context, clusterNames []string) (map[string]string, error) {
	engine, basePath := utils.ParseVaultPath(p.cfg.KubeconfigsPath)

	type result struct {
		cluster string
		secret  *vaultapi.KVSecret
		err     error
	}

	resultsCh := make(chan result, len(clusterNames))
	var wg sync.WaitGroup
	for _, cluster := range clusterNames {
		cluster := cluster
		wg.Add(1)
		go func() {
			defer wg.Done()
			secret, err := readSecret(ctx, p.client, p.cfg.Timeout, engine, fmt.Sprintf("%s/%s", basePath, cluster))
			resultsCh <- result{cluster: cluster, secret: secret, err: err}
		}()
	}
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	kubeconfigs := make(map[string]string, len(clusterNames))
	for res := range resultsCh {
		if res.err != nil {
			return nil, fmt.Errorf("couldn't read kubeconfig for cluster %s from Vault: %w", res.cluster, res.err)
		}
		kubeconfig, ok := res.secret.Data[p.cfg.KubeconfigKey].(string)
		if !ok {
			return nil, fmt.Errorf("kubeconfig secret for cluster %s has no string field %q", res.cluster, p.cfg.KubeconfigKey)
		}
		kubeconfigs[res.cluster] = kubeconfig
	}
	return kubeconfigs, nil
}
