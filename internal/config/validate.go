package config

import (
	"fmt"
	"slices"

	log "github.com/sirupsen/logrus"
)

func (c *CleanerConfig) Validate() error {
	if c.PushedDaysAgo < 9 {
		return fmt.Errorf("pushed-days-ago cannot be less than 9 days, got %d", c.PushedDaysAgo)
	}
	if c.TopAge < 1 {
		return fmt.Errorf("top-age cannot be less than 1, got %d", c.TopAge)
	}

	switch c.WorkloadSource {
	case "k8s", "none":
	default:
		return fmt.Errorf("workload-source must be k8s or none, got %q", c.WorkloadSource)
	}

	switch c.DeleteMode {
	case "dry-run":
		log.Info("Using dry-run: artifacts will not be deleted")
	case "soft-delete":
		if slices.Contains(c.ProjectsToClean, c.GarbageProjectName) {
			return fmt.Errorf("projects-to-clean %v contains the garbage project %q", c.ProjectsToClean, c.GarbageProjectName)
		}
		log.Infof("Using soft-delete: deleted artifacts will be moved to project %q", c.GarbageProjectName)
	case "hard-delete":
		log.Warn("Using hard-delete: deleted artifacts are gone for good")
	default:
		return fmt.Errorf("delete-mode must be dry-run, soft-delete or hard-delete, got %q", c.DeleteMode)
	}

	switch c.SecretsProvider {
	case "vault", "env":
	default:
		return fmt.Errorf("secrets-provider must be vault or env, got %q", c.SecretsProvider)
	}

	switch c.VaultAuthMode {
	case "jwt", "token":
	default:
		return fmt.Errorf("vault-auth-mode must be jwt or token, got %q", c.VaultAuthMode)
	}

	if c.WorkloadSource == "k8s" {
		switch c.K8sAuthMode {
		case "vault":
			if c.SecretsProvider != "vault" {
				return fmt.Errorf("k8s-auth-mode=vault requires secrets-provider=vault (got %q)", c.SecretsProvider)
			}
		case "local-kubeconfig":
			if c.K8sLocalKubeconfigPath == "" {
				return fmt.Errorf("k8s-auth-mode=local-kubeconfig requires k8s-local-kubeconfig-path to be set")
			}
		case "in-cluster":
		default:
			return fmt.Errorf("k8s-auth-mode must be vault, local-kubeconfig or in-cluster, got %q", c.K8sAuthMode)
		}
		if len(c.Clusters) == 0 && c.K8sAuthMode != "in-cluster" {
			return fmt.Errorf("workload-source=k8s requires at least one entry in clusters (unless k8s-auth-mode=in-cluster)")
		}
	}

	if c.HarborTimeoutMinutes < 7 {
		log.Warnf("harbor-timeout-minutes is less than 7 minutes (%d); may not be enough for large registries", c.HarborTimeoutMinutes)
	}
	if c.VaultTimeoutMinutes < 1 {
		log.Warnf("vault-timeout-minutes is less than 1 minute (%d); may not be enough when fetching many secrets", c.VaultTimeoutMinutes)
	}
	if c.K8sTimeoutMinutes < 5 {
		log.Warnf("k8s-timeout-minutes is less than 5 minutes (%d); may not be enough for many clusters", c.K8sTimeoutMinutes)
	}
	if c.CleaningTimeoutMinutes < 20 {
		log.Warnf("cleaning-timeout-minutes is less than 20 minutes (%d); may not be enough for soft-delete on large projects", c.CleaningTimeoutMinutes)
	}

	log.Infof("Using %q as workload source, %q as secrets provider", c.WorkloadSource, c.SecretsProvider)
	return nil
}
