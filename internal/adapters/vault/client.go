// Package vault implements ports.SecretsProvider against HashiCorp Vault.
package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// NewClientWithJWT authenticates against Vault's JWT auth backend (e.g. the
// OIDC ID token a CI platform like GitLab CI issues for a job) and returns a
// ready-to-use client. authMountPath is the Vault auth backend's login path,
// e.g. "auth/gitlab_jwt/login" for a backend mounted at "gitlab_jwt".
func NewClientWithJWT(address, authMountPath, role, jwt string, timeout time.Duration) (*vaultapi.Client, error) {
	config := vaultapi.DefaultConfig()
	config.Address = address
	config.Timeout = timeout

	client, err := vaultapi.NewClient(config)
	if err != nil {
		return nil, err
	}

	token, err := loginWithJWT(address, authMountPath, role, jwt)
	if err != nil {
		return nil, fmt.Errorf("couldn't get vault token with JWT: %w", err)
	}
	client.SetToken(token)

	if err := checkConnectivity(client); err != nil {
		return nil, err
	}
	return client, nil
}

// NewClientWithToken authenticates using a static token read from the
// VAULT_TOKEN environment variable - the simplest way to run against Vault
// outside of CI (local development, integration tests).
func NewClientWithToken(address string, timeout time.Duration) (*vaultapi.Client, error) {
	config := vaultapi.DefaultConfig()
	config.Address = address
	config.Timeout = timeout

	client, err := vaultapi.NewClient(config)
	if err != nil {
		return nil, err
	}

	token, ok := os.LookupEnv("VAULT_TOKEN")
	if !ok {
		return nil, fmt.Errorf("VAULT_TOKEN environment variable is not set")
	}
	client.SetToken(token)

	if err := checkConnectivity(client); err != nil {
		return nil, err
	}
	return client, nil
}

func checkConnectivity(client *vaultapi.Client) error {
	log.Info("Checking connection to Vault...")
	_, err := client.Sys().Health()
	return err
}

func loginWithJWT(address, authMountPath, role, jwt string) (string, error) {
	payload, err := json.Marshal(map[string]string{"role": role, "jwt": jwt})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, address+"/v1/"+authMountPath, bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	token := gjson.GetBytes(body, "auth.client_token").String()
	if token == "" {
		return "", fmt.Errorf("vault login response did not contain auth.client_token: %s", string(body))
	}
	return token, nil
}

// readSecret reads a single KVv2 secret.
func readSecret(ctx context.Context, client *vaultapi.Client, timeout time.Duration, engine, path string) (*vaultapi.KVSecret, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return client.KVv2(engine).Get(ctxWithTimeout, path)
}
