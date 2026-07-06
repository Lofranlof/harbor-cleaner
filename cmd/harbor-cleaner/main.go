// harbor-cleaner is a retention/garbage-collection tool for a Harbor container
// registry. This file is the composition root: it is the only place in the
// codebase that knows about every adapter (Harbor, Vault, env vars, Kubernetes)
// and wires the concrete ones the config asks for into internal/app.Cleaner,
// which only ever sees them through the internal/ports interfaces.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"harbor-cleaner/internal/adapters/envsecrets"
	harboradapter "harbor-cleaner/internal/adapters/harbor"
	k8sadapter "harbor-cleaner/internal/adapters/k8s"
	"harbor-cleaner/internal/adapters/noworkload"
	vaultadapter "harbor-cleaner/internal/adapters/vault"
	"harbor-cleaner/internal/app"
	"harbor-cleaner/internal/config"
	"harbor-cleaner/internal/ports"

	vaultapi "github.com/hashicorp/vault/api"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

func main() {
	cfg := config.InitConfig()
	configureLogger(cfg)

	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	registryURL, err := url.Parse(cfg.HarborURLApi)
	if err != nil {
		log.Fatalf("Couldn't parse harbor-url-api: %v", err)
	}

	secretsProvider, err := buildSecretsProvider(cfg)
	if err != nil {
		log.Fatal(err)
	}

	creds, err := secretsProvider.HarborCredentials(ctx)
	if err != nil {
		log.Fatalf("Couldn't get Harbor credentials: %v", err)
	}

	harborCfg := harboradapter.Config{
		URL:                       registryURL,
		PageSize:                  cfg.PageSize,
		NumOfWorkersAllProjects:   cfg.NumOfWorkersAllProjects,
		NumOfWorkersAllRepos:      cfg.NumOfWorkersAllRepos,
		NumOfWorkersProjectRepos:  cfg.NumOfWorkersProjectRepos,
		NumOfWorkersRepoArtifacts: cfg.NumOfWorkersRepoArtifacts,
		Timeout:                   time.Duration(cfg.HarborTimeoutMinutes) * time.Minute,
		InsecureSkipVerify:        cfg.HarborInsecureSkipVerify,
	}
	harborClient, err := harboradapter.NewClient(harborCfg, creds.Login, creds.Password)
	if err != nil {
		log.Fatalf("Couldn't init Harbor client: %v", err)
	}
	registry := harboradapter.NewRegistry(harborClient, harborCfg)

	workload, err := buildWorkloadSource(ctx, cfg, secretsProvider)
	if err != nil {
		log.Fatal(err)
	}

	cleaner := app.NewCleaner(app.Options{
		ProjectsToClean:      cfg.ProjectsToClean,
		ProjectsToPreserve:   cfg.ProjectsToPreserve,
		ReposToPreserve:      cfg.ReposToPreserve,
		PushedDaysAgo:        cfg.PushedDaysAgo,
		TopAge:               cfg.TopAge,
		DeleteMode:           cfg.DeleteMode,
		GarbageProjectName:   cfg.GarbageProjectName,
		RegistryHost:         registryURL.Host,
		NumOfWorkersCleaning: cfg.NumOfWorkersArtifactsCleaning,
	}, registry, workload)

	log.Info("Fetching info from Harbor, please stand by...")
	if err := cleaner.Collect(ctx); err != nil {
		log.Fatal(err)
	}
	log.Info("Done fetching from Harbor")

	log.Info("Preserving necessary images, please stand by...")
	if err := cleaner.Preserve(ctx); err != nil {
		log.Fatal(err)
	}
	log.Info("Done preserving images")

	logCleaningPlan(registryURL.Host, cleaner)

	cleanCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.CleaningTimeoutMinutes)*time.Minute)
	defer cancel()
	if err := cleaner.Clean(cleanCtx); err != nil {
		log.Fatal(err)
	}
	log.Info("Done cleaning images")
}

func buildSecretsProvider(cfg *config.CleanerConfig) (ports.SecretsProvider, error) {
	switch cfg.SecretsProvider {
	case "vault":
		timeout := time.Duration(cfg.VaultTimeoutMinutes) * time.Minute
		client, err := buildVaultClient(cfg, timeout)
		if err != nil {
			return nil, err
		}
		return vaultadapter.NewSecretsProvider(client, vaultadapter.Config{
			Timeout:           timeout,
			HarborCredsPath:   cfg.VaultHarborCredsPath,
			HarborLoginKey:    cfg.VaultHarborLoginSecretName,
			HarborPasswordKey: cfg.VaultHarborPasswordSecretName,
			KubeconfigsPath:   cfg.VaultKubeconfigsPath,
			KubeconfigKey:     "kubeconfig",
		}), nil
	case "env":
		return envsecrets.NewSecretsProvider(envsecrets.Config{
			HarborLoginEnvVar:    cfg.VaultHarborLoginSecretName,
			HarborPasswordEnvVar: cfg.VaultHarborPasswordSecretName,
		}), nil
	default:
		return nil, fmt.Errorf("unknown secrets-provider: %s", cfg.SecretsProvider)
	}
}

func buildVaultClient(cfg *config.CleanerConfig, timeout time.Duration) (*vaultapi.Client, error) {
	switch cfg.VaultAuthMode {
	case "jwt":
		return vaultadapter.NewClientWithJWT(cfg.VaultURL, cfg.VaultAuthMountPath, cfg.VaultAuthRole, cfg.VaultJWT, timeout)
	case "token":
		return vaultadapter.NewClientWithToken(cfg.VaultURL, timeout)
	default:
		return nil, fmt.Errorf("unknown vault-auth-mode: %s", cfg.VaultAuthMode)
	}
}

func buildWorkloadSource(ctx context.Context, cfg *config.CleanerConfig, secretsProvider ports.SecretsProvider) (ports.WorkloadSource, error) {
	switch cfg.WorkloadSource {
	case "none":
		return noworkload.New(), nil
	case "k8s":
		clientsets, err := buildK8sClientsets(ctx, cfg, secretsProvider)
		if err != nil {
			return nil, err
		}
		return k8sadapter.NewWorkloadSource(clientsets, time.Duration(cfg.K8sTimeoutMinutes)*time.Minute), nil
	default:
		return nil, fmt.Errorf("unknown workload-source: %s", cfg.WorkloadSource)
	}
}

func buildK8sClientsets(ctx context.Context, cfg *config.CleanerConfig, secretsProvider ports.SecretsProvider) ([]kubernetes.Interface, error) {
	switch cfg.K8sAuthMode {
	case "vault":
		kubeconfigs, err := secretsProvider.Kubeconfigs(ctx, cfg.Clusters)
		if err != nil {
			return nil, fmt.Errorf("couldn't get kubeconfigs: %w", err)
		}
		clientsets := make([]kubernetes.Interface, 0, len(cfg.Clusters))
		for _, cluster := range cfg.Clusters {
			cs, err := k8sadapter.NewClientFromKubeconfigString(kubeconfigs[cluster])
			if err != nil {
				return nil, fmt.Errorf("couldn't build k8s client for cluster %s: %w", cluster, err)
			}
			clientsets = append(clientsets, cs)
		}
		return clientsets, nil
	case "local-kubeconfig":
		cs, err := k8sadapter.NewClientFromLocalKubeconfig(cfg.K8sLocalKubeconfigPath)
		if err != nil {
			return nil, err
		}
		return []kubernetes.Interface{cs}, nil
	case "in-cluster":
		cs, err := k8sadapter.NewInClusterClient()
		if err != nil {
			return nil, err
		}
		return []kubernetes.Interface{cs}, nil
	default:
		return nil, fmt.Errorf("unknown k8s-auth-mode: %s", cfg.K8sAuthMode)
	}
}

func logCleaningPlan(registryHost string, cleaner *app.Cleaner) {
	var cleanedSizeBytes int64
	for _, proj := range cleaner.Projects() {
		for _, repo := range proj.Repos {
			for _, art := range repo.Artifacts {
				if art.Preserve {
					continue
				}
				cleanedSizeBytes += art.SizeBytes
				tags := make([]string, 0, len(art.Tags))
				for _, t := range art.Tags {
					tags = append(tags, t.Name)
				}
				log.Infof("Artifact to be cleaned: %s/%s@%s, tags: %v", registryHost, repo.Name, art.Digest, tags)
			}
		}
	}
	log.Infof("Cleaning unpreserved images. Total size marked for deletion: %d GB (overestimate of actual reclaimed space)", cleanedSizeBytes/1024/1024/1024)
}

func configureLogger(cfg *config.CleanerConfig) {
	lvl, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.Fatal(err)
	}
	log.SetLevel(lvl)
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	switch cfg.LogTarget {
	case "file":
		if cfg.LogPath == "" {
			log.Fatalf("log-target is file, but log-path is empty")
		}
		file, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("Couldn't open log file: %v", err)
		}
		log.SetOutput(file)
	case "stdout":
	default:
		log.Fatalf("Unknown log-target: %s (must be file or stdout)", cfg.LogTarget)
	}
}
