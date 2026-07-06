// Package config loads harbor-cleaner's configuration from a YAML file plus
// CLI-flag/env overrides. It has no knowledge of domain/ports/adapters -
// the composition root (cmd/harbor-cleaner) is what turns a CleanerConfig into
// concrete adapters.
package config

import (
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// CleanerConfig holds every configurable knob. Fields tagged `mapstructure`
// can be set from the YAML config file; most are also overridable via the
// matching --flag (see InitConfig).
type CleanerConfig struct {
	// --- what to clean ---
	ProjectsToClean    []string `mapstructure:"projects-to-clean"`    // Harbor projects to scan; ["all"] scans the whole registry
	ProjectsToPreserve []string `mapstructure:"projects-to-preserve"` // Harbor projects whose artifacts are always kept
	ReposToPreserve    []string `mapstructure:"repos-to-preserve"`    // Harbor repos (full "<project>/<repo>" name) whose artifacts are always kept
	PushedDaysAgo      int      `mapstructure:"pushed-days-ago"`      // artifacts pushed less than this many days ago are always kept (minimum 9)
	TopAge             int      `mapstructure:"top-age"`              // the N freshest artifacts per repo are always kept

	// --- Harbor ---
	HarborURLApi              string `mapstructure:"harbor-url-api"`
	HarborInsecureSkipVerify  bool   `mapstructure:"harbor-insecure-skip-verify"` // skip TLS verification; only for local/demo Harbor with a self-signed cert
	PageSize                  int64  `mapstructure:"page-size"`                   // Harbor API page size, max 100
	NumOfWorkersAllProjects   int    `mapstructure:"num-of-workers-all-projects"`
	NumOfWorkersAllRepos      int    `mapstructure:"num-of-workers-all-repos"`
	NumOfWorkersProjectRepos  int    `mapstructure:"num-of-workers-project-repos"`
	NumOfWorkersRepoArtifacts int    `mapstructure:"num-of-workers-repo-artifacts"`
	HarborTimeoutMinutes      int    `mapstructure:"harbor-timeout-minutes"`

	// --- deletion ---
	DeleteMode                    string `mapstructure:"delete-mode"` // dry-run | soft-delete | hard-delete
	GarbageProjectName            string `mapstructure:"garbage-project-name"`
	NumOfWorkersArtifactsCleaning int    `mapstructure:"num-of-workers-artifacts-cleaning"`
	CleaningTimeoutMinutes        int    `mapstructure:"cleaning-timeout-minutes"`

	// --- secrets ---
	SecretsProvider               string `mapstructure:"secrets-provider"` // vault | env
	VaultURL                      string `mapstructure:"vault-url"`
	VaultTimeoutMinutes           int    `mapstructure:"vault-timeout-minutes"`
	VaultAuthMode                 string `mapstructure:"vault-auth-mode"`       // jwt | token (token reads $VAULT_TOKEN)
	VaultAuthRole                 string `mapstructure:"vault-auth-role"`       // Vault role bound to the JWT auth backend
	VaultAuthMountPath            string `mapstructure:"vault-auth-mount-path"` // e.g. "auth/jwt/login"
	VaultJWT                      string // sensitive, flag-only, never persisted to YAML
	VaultHarborCredsPath          string `mapstructure:"vault-harbor-creds-path-fullpath"`
	VaultHarborLoginSecretName    string `mapstructure:"vault-harbor-login-secret-name"`
	VaultHarborPasswordSecretName string `mapstructure:"vault-harbor-password-secret-name"`
	VaultKubeconfigsPath          string `mapstructure:"vault-kubeconfigs-fullpath"`

	// --- workload source (what "currently in use" means) ---
	WorkloadSource         string   `mapstructure:"workload-source"`           // k8s | none
	Clusters               []string `mapstructure:"clusters"`                  // cluster names to inspect when WorkloadSource == k8s
	K8sAuthMode            string   `mapstructure:"k8s-auth-mode"`             // vault | local-kubeconfig | in-cluster
	K8sLocalKubeconfigPath string   `mapstructure:"k8s-local-kubeconfig-path"` // used when K8sAuthMode == local-kubeconfig
	K8sTimeoutMinutes      int      `mapstructure:"k8s-timeout-minutes"`

	// --- misc ---
	LogLevel   string `mapstructure:"log-level"`
	LogTarget  string `mapstructure:"log-target"` // stdout | file
	LogPath    string `mapstructure:"log-path"`
	ConfigName string
}

// InitConfig registers CLI flags, binds them into viper, loads the YAML file
// named by --config-name from ./configs (or ../configs, ../../configs, ...),
// and lets a handful of list-typed flags override the YAML's values.
func InitConfig() *CleanerConfig {
	cc := &CleanerConfig{}

	pflag.String("log-level", "info", "Logging level")
	pflag.String("log-target", "stdout", "Where to write the log: stdout or file")
	pflag.String("log-path", "./harbor-cleaner.log", "Path to the log file, if log-target is file")
	pflag.String("config-name", "example", "Name of the config file to use (without .yml), looked up under ./configs")
	pflag.String("projects-to-clean", "", "Comma-separated list of projects to clean, or \"all\"")
	pflag.String("repos-to-preserve", "none", "Comma-separated list of repos (project/repo) to always preserve")
	pflag.Int("pushed-days-ago", 90, "Artifacts younger than this many days are always kept")
	pflag.Int("top-age", 3, "The N freshest artifacts per repo are always kept")
	pflag.String("workload-source", "none", "Source of \"currently in use\" images: k8s or none")
	pflag.String("delete-mode", "dry-run", "dry-run (GET only), soft-delete (move to garbage project), or hard-delete")
	pflag.String("secrets-provider", "vault", "Where to read Harbor credentials from: vault or env")
	pflag.String("vault-jwt", "", "JWT token used to authenticate against Vault's JWT auth backend (vault-auth-mode=jwt)")
	pflag.String("vault-harbor-login-secret-name", "HARBOR_REGISTRY_USER_RO", "Name of the field/env var holding the Harbor username")
	pflag.String("vault-harbor-password-secret-name", "HARBOR_REGISTRY_PASSWORD_RO", "Name of the field/env var holding the Harbor password")
	pflag.Int("harbor-timeout-minutes", 30, "Timeout for Harbor requests (fetching projects/repos/artifacts)")
	pflag.Int("vault-timeout-minutes", 5, "Timeout for Vault requests (fetching secrets)")
	pflag.Int("k8s-timeout-minutes", 5, "Timeout for Kubernetes requests (listing workloads)")
	pflag.Int("cleaning-timeout-minutes", 60, "Timeout for the cleaning phase")
	pflag.Int("num-of-workers-all-projects", 1, "Concurrent goroutines fetching all Harbor projects")
	pflag.Int("num-of-workers-all-repos", 30, "Concurrent goroutines fetching all Harbor repositories")
	pflag.Int("num-of-workers-repo-artifacts", 2, "Concurrent goroutines fetching artifacts within one repo")
	pflag.Int("num-of-workers-artifacts-cleaning", 30, "Concurrent goroutines deleting/moving artifacts")
	pflag.Int("num-of-workers-project-repos", 3, "Concurrent goroutines fetching repositories within one project")
	pflag.Parse()
	if err := viper.BindPFlags(pflag.CommandLine); err != nil {
		log.Fatalf("Couldn't bind flags: %v", err)
	}

	viper.SetConfigName(viper.GetString("config-name"))
	viper.SetConfigType("yml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath("../configs")
	viper.AddConfigPath("../../configs")
	viper.AddConfigPath("../../../configs")

	viper.SetDefault("pushed-days-ago", 90)
	viper.SetDefault("log-level", "info")
	viper.SetDefault("log-target", "stdout")
	viper.SetDefault("log-path", "./harbor-cleaner.log")
	viper.SetDefault("garbage-project-name", "trashcan")
	viper.SetDefault("delete-mode", "dry-run")
	viper.SetDefault("secrets-provider", "vault")
	viper.SetDefault("vault-auth-mode", "jwt")
	viper.SetDefault("vault-auth-mount-path", "auth/jwt/login")
	viper.SetDefault("k8s-auth-mode", "vault")
	viper.SetDefault("workload-source", "none")

	viper.Set("VaultJWT", viper.GetString("vault-jwt"))

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Fatal error reading config file: %v", err)
	}
	if err := viper.Unmarshal(cc); err != nil {
		log.Fatalf("Fatal error unmarshalling config: %v", err)
	}

	if projectsToClean := viper.GetString("projects-to-clean"); projectsToClean != "" {
		cc.ProjectsToClean = strings.Split(projectsToClean, ",")
	}
	if reposToPreserve := viper.GetString("repos-to-preserve"); reposToPreserve != "none" {
		cc.ReposToPreserve = strings.Split(reposToPreserve, ",")
	}

	return cc
}
