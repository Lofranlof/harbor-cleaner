package k8s

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

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

func TestNewClientFromKubeconfigStringDecodesValidKubeconfig(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(minimalKubeconfig))

	clientset, err := NewClientFromKubeconfigString(encoded)

	require.NoError(t, err)
	assert.NotNil(t, clientset)
}

func TestNewClientFromKubeconfigStringRejectsInvalidBase64(t *testing.T) {
	_, err := NewClientFromKubeconfigString("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestNewClientFromKubeconfigStringRejectsInvalidKubeconfigContent(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("not a kubeconfig"))
	_, err := NewClientFromKubeconfigString(encoded)
	assert.Error(t, err)
}

func TestNewClientFromLocalKubeconfigReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(minimalKubeconfig), 0600))

	clientset, err := NewClientFromLocalKubeconfig(path)

	require.NoError(t, err)
	assert.NotNil(t, clientset)
}

func TestNewClientFromLocalKubeconfigRejectsMissingFile(t *testing.T) {
	_, err := NewClientFromLocalKubeconfig(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.Error(t, err)
}

func TestNewInClusterClientErrorsOutsideACluster(t *testing.T) {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		t.Skip("running inside a Kubernetes pod, in-cluster config would actually succeed")
	}
	_, err := NewInClusterClient()
	assert.Error(t, err)
}
