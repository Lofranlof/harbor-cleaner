package envsecrets

import (
	"context"
	"testing"

	"harbor-cleaner/internal/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarborCredentialsReadsBothEnvVars(t *testing.T) {
	t.Setenv("TEST_HARBOR_LOGIN", "admin")
	t.Setenv("TEST_HARBOR_PASSWORD", "hunter2")

	p := NewSecretsProvider(Config{
		HarborLoginEnvVar:    "TEST_HARBOR_LOGIN",
		HarborPasswordEnvVar: "TEST_HARBOR_PASSWORD",
	})

	creds, err := p.HarborCredentials(context.Background())

	require.NoError(t, err)
	assert.Equal(t, ports.HarborCredentials{Login: "admin", Password: "hunter2"}, creds)
}

func TestHarborCredentialsErrorsWhenLoginVarMissing(t *testing.T) {
	t.Setenv("TEST_HARBOR_PASSWORD", "hunter2")

	p := NewSecretsProvider(Config{
		HarborLoginEnvVar:    "TEST_HARBOR_LOGIN_MISSING",
		HarborPasswordEnvVar: "TEST_HARBOR_PASSWORD",
	})

	_, err := p.HarborCredentials(context.Background())
	assert.Error(t, err)
}

func TestHarborCredentialsErrorsWhenPasswordVarMissing(t *testing.T) {
	t.Setenv("TEST_HARBOR_LOGIN", "admin")

	p := NewSecretsProvider(Config{
		HarborLoginEnvVar:    "TEST_HARBOR_LOGIN",
		HarborPasswordEnvVar: "TEST_HARBOR_PASSWORD_MISSING",
	})

	_, err := p.HarborCredentials(context.Background())
	assert.Error(t, err)
}

func TestKubeconfigsAlwaysErrors(t *testing.T) {
	p := NewSecretsProvider(Config{})

	_, err := p.Kubeconfigs(context.Background(), []string{"any-cluster"})
	assert.Error(t, err)
}
