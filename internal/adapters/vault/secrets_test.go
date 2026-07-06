package vault

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"harbor-cleaner/internal/ports"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKVv2Server serves a KV v2 secrets engine mounted at "secret", backed by
// an in-memory map keyed by the path within the engine.
func fakeKVv2Server(t *testing.T, secrets map[string]map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/v1/secret/data/"
		if len(r.URL.Path) <= len(prefix) || r.URL.Path[:len(prefix)] != prefix {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		path := r.URL.Path[len(prefix):]
		data, ok := secrets[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"data":%s,"metadata":{"version":1}}}`, toJSON(t, data))
	}))
}

func toJSON(t *testing.T, m map[string]interface{}) string {
	t.Helper()
	out := "{"
	first := true
	for k, v := range m {
		if !first {
			out += ","
		}
		first = false
		out += fmt.Sprintf("%q:%q", k, v)
	}
	out += "}"
	return out
}

func newTestProvider(t *testing.T, srvURL string, cfg Config) *SecretsProvider {
	t.Helper()
	vaultCfg := vaultapi.DefaultConfig()
	vaultCfg.Address = srvURL
	client, err := vaultapi.NewClient(vaultCfg)
	require.NoError(t, err)
	return NewSecretsProvider(client, cfg)
}

func TestHarborCredentialsReadsFromVault(t *testing.T) {
	srv := fakeKVv2Server(t, map[string]map[string]interface{}{
		"harbor-cleaner": {"HARBOR_USER": "admin", "HARBOR_PASS": "hunter2"},
	})
	defer srv.Close()

	p := newTestProvider(t, srv.URL, Config{
		Timeout:           5 * time.Second,
		HarborCredsPath:   "secret/harbor-cleaner",
		HarborLoginKey:    "HARBOR_USER",
		HarborPasswordKey: "HARBOR_PASS",
	})

	creds, err := p.HarborCredentials(context.Background())

	require.NoError(t, err)
	assert.Equal(t, ports.HarborCredentials{Login: "admin", Password: "hunter2"}, creds)
}

func TestHarborCredentialsErrorsWhenSecretMissing(t *testing.T) {
	srv := fakeKVv2Server(t, map[string]map[string]interface{}{})
	defer srv.Close()

	p := newTestProvider(t, srv.URL, Config{
		Timeout:         5 * time.Second,
		HarborCredsPath: "secret/does-not-exist",
	})

	_, err := p.HarborCredentials(context.Background())
	assert.Error(t, err)
}

func TestHarborCredentialsErrorsWhenFieldMissing(t *testing.T) {
	srv := fakeKVv2Server(t, map[string]map[string]interface{}{
		"harbor-cleaner": {"HARBOR_USER": "admin"},
	})
	defer srv.Close()

	p := newTestProvider(t, srv.URL, Config{
		Timeout:           5 * time.Second,
		HarborCredsPath:   "secret/harbor-cleaner",
		HarborLoginKey:    "HARBOR_USER",
		HarborPasswordKey: "HARBOR_PASS",
	})

	_, err := p.HarborCredentials(context.Background())
	assert.Error(t, err)
}

func TestKubeconfigsReadsOneSecretPerClusterConcurrently(t *testing.T) {
	srv := fakeKVv2Server(t, map[string]map[string]interface{}{
		"kubeconfigs/cluster-a": {"kubeconfig": "kubeconfig-a-content"},
		"kubeconfigs/cluster-b": {"kubeconfig": "kubeconfig-b-content"},
	})
	defer srv.Close()

	p := newTestProvider(t, srv.URL, Config{
		Timeout:         5 * time.Second,
		KubeconfigsPath: "secret/kubeconfigs",
		KubeconfigKey:   "kubeconfig",
	})

	kubeconfigs, err := p.Kubeconfigs(context.Background(), []string{"cluster-a", "cluster-b"})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"cluster-a": "kubeconfig-a-content",
		"cluster-b": "kubeconfig-b-content",
	}, kubeconfigs)
}

func TestKubeconfigsErrorsWhenOneClusterSecretMissing(t *testing.T) {
	srv := fakeKVv2Server(t, map[string]map[string]interface{}{
		"kubeconfigs/cluster-a": {"kubeconfig": "kubeconfig-a-content"},
	})
	defer srv.Close()

	p := newTestProvider(t, srv.URL, Config{
		Timeout:         5 * time.Second,
		KubeconfigsPath: "secret/kubeconfigs",
		KubeconfigKey:   "kubeconfig",
	})

	_, err := p.Kubeconfigs(context.Background(), []string{"cluster-a", "cluster-missing"})
	assert.Error(t, err)
}
