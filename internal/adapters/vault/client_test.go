package vault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginWithJWTReturnsTokenOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/auth/jwt/login", r.URL.Path)

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "my-role", body["role"])
		assert.Equal(t, "my-jwt", body["jwt"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"client_token":"s.abc123"}}`))
	}))
	defer srv.Close()

	token, err := loginWithJWT(srv.URL, "auth/jwt/login", "my-role", "my-jwt")

	require.NoError(t, err)
	assert.Equal(t, "s.abc123", token)
}

func TestLoginWithJWTErrorsWhenResponseHasNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	defer srv.Close()

	_, err := loginWithJWT(srv.URL, "auth/jwt/login", "my-role", "my-jwt")
	assert.Error(t, err)
}

func TestLoginWithJWTErrorsWhenServerUnreachable(t *testing.T) {
	_, err := loginWithJWT("http://127.0.0.1:1", "auth/jwt/login", "my-role", "my-jwt")
	assert.Error(t, err)
}

func TestCheckConnectivitySucceedsWhenVaultIsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"initialized":true,"sealed":false,"standby":false}`))
	}))
	defer srv.Close()

	client := newTestVaultClient(t, srv.URL)
	assert.NoError(t, checkConnectivity(client))
}

func TestCheckConnectivityFailsWhenVaultUnreachable(t *testing.T) {
	client := newTestVaultClient(t, "http://127.0.0.1:1")
	assert.Error(t, checkConnectivity(client))
}

func TestNewClientWithTokenErrorsWhenEnvVarMissing(t *testing.T) {
	require.NoError(t, os.Unsetenv("VAULT_TOKEN"))

	_, err := NewClientWithToken("http://127.0.0.1:1", 0)
	assert.Error(t, err)
}

func newTestVaultClient(t *testing.T, address string) *vaultapi.Client {
	t.Helper()
	cfg := vaultapi.DefaultConfig()
	cfg.Address = address
	client, err := vaultapi.NewClient(cfg)
	require.NoError(t, err)
	return client
}
