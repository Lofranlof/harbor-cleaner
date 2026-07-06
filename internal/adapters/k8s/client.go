// Package k8s implements ports.WorkloadSource by inspecting workload
// controllers (Deployments, StatefulSets, DaemonSets, Jobs, CronJobs) running
// in one or more Kubernetes clusters.
package k8s

import (
	"encoding/base64"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientFromKubeconfigString builds a clientset from a base64-encoded
// kubeconfig, as fetched from a secret store such as Vault.
func NewClientFromKubeconfigString(encodedKubeconfig string) (kubernetes.Interface, error) {
	decoded, err := base64.StdEncoding.DecodeString(encodedKubeconfig)
	if err != nil {
		return nil, err
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(decoded)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}

// NewClientFromLocalKubeconfig builds a clientset from a kubeconfig file on
// disk - the usual way to run the tool from a workstation or CI runner that
// already has cluster access configured.
func NewClientFromLocalKubeconfig(path string) (kubernetes.Interface, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}

// NewInClusterClient builds a clientset from the service account Kubernetes
// injects into every pod - for running the tool as a workload inside the
// cluster it's meant to inspect.
func NewInClusterClient() (kubernetes.Interface, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}
