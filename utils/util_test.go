package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetRepoNameWithinProject(t *testing.T) {
	tests := []struct {
		name                string
		repoNameWithProject string
		wantRepoName        string
	}{
		{
			name:                "Test #1",
			repoNameWithProject: "myproject/myteam/myapp",
			wantRepoName:        "myteam/myapp",
		},
		{
			name:                "Test #2",
			repoNameWithProject: "team-a/spring-cloud-config-server",
			wantRepoName:        "spring-cloud-config-server",
		},
		{
			name:                "Test #3",
			repoNameWithProject: "team-b/mlops/openllm-k8s/dba-update",
			wantRepoName:        "mlops/openllm-k8s/dba-update",
		},
		{
			name:                "Test #4",
			repoNameWithProject: "myorg/workspace/warehouses/wh-processor-cassandra-migration",
			wantRepoName:        "workspace/warehouses/wh-processor-cassandra-migration",
		},
		{
			name:                "Test #5",
			repoNameWithProject: "myorg/workspace/allow-mode/am_statistics_processor-cassandra-migration",
			wantRepoName:        "workspace/allow-mode/am_statistics_processor-cassandra-migration",
		},
		{
			name:                "Test #6",
			repoNameWithProject: "analytics/kyuubi",
			wantRepoName:        "kyuubi",
		},
		{
			name:                "Test #7",
			repoNameWithProject: "team-c_easy-report/easy-report/easy-report-clj/some-bot",
			wantRepoName:        "easy-report/easy-report-clj/some-bot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoName := GetRepoNameWithinProject(tt.repoNameWithProject)
			assert.Equal(t, tt.wantRepoName, repoName)
		})
	}
}

func TestGetProjectNameOfRepository(t *testing.T) {
	tests := []struct {
		name                string
		repoNameWithProject string
		wantProjectName     string
	}{
		{
			name:                "Test #1",
			repoNameWithProject: "myproject/myteam/myapp",
			wantProjectName:     "myproject",
		},
		{
			name:                "Test #2",
			repoNameWithProject: "team-a/spring-cloud-config-server",
			wantProjectName:     "team-a",
		},
		{
			name:                "Test #3",
			repoNameWithProject: "team-b/mlops/openllm-k8s/dba-update",
			wantProjectName:     "team-b",
		},
		{
			name:                "Test #4",
			repoNameWithProject: "myorg/workspace/warehouses/wh-processor-cassandra-migration",
			wantProjectName:     "myorg",
		},
		{
			name:                "Test #5",
			repoNameWithProject: "myorg/workspace/allow-mode/am_statistics_processor-cassandra-migration",
			wantProjectName:     "myorg",
		},
		{
			name:                "Test #6",
			repoNameWithProject: "analytics/kyuubi",
			wantProjectName:     "analytics",
		},
		{
			name:                "Test #7",
			repoNameWithProject: "team-c_easy-report/easy-report/easy-report-clj/some-bot",
			wantProjectName:     "team-c_easy-report",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectName := GetProjectNameOfRepository(tt.repoNameWithProject)
			assert.Equal(t, tt.wantProjectName, projectName)
		})
	}
}

func TestParseVaultPath(t *testing.T) {
	tests := []struct {
		name                 string
		vaultFullPath        string
		wantEngine           string
		wantPathWithinEngine string
	}{
		{
			name:                 "Test #1",
			vaultFullPath:        "COMMON/kubeconfig/dev-cluster",
			wantEngine:           "COMMON",
			wantPathWithinEngine: "kubeconfig/dev-cluster",
		},
		{
			name:                 "Test #2",
			vaultFullPath:        "COMMON/kubeconfig/staging-cluster",
			wantEngine:           "COMMON",
			wantPathWithinEngine: "kubeconfig/staging-cluster",
		},
		{
			name:                 "Test #3",
			vaultFullPath:        "COMMON/registry/harbor",
			wantEngine:           "COMMON",
			wantPathWithinEngine: "registry/harbor",
		},
		{
			name:                 "Test #4",
			vaultFullPath:        "TEAM/qa-cluster/k8s/secrets/database",
			wantEngine:           "TEAM",
			wantPathWithinEngine: "qa-cluster/k8s/secrets/database",
		},
		{
			name:                 "Test #5",
			vaultFullPath:        "009/009-dev01/apps/python",
			wantEngine:           "009",
			wantPathWithinEngine: "009-dev01/apps/python",
		},
		{
			name:                 "Test #6",
			vaultFullPath:        "009/009-dev01/s3",
			wantEngine:           "009",
			wantPathWithinEngine: "009-dev01/s3",
		},
		{
			name:                 "Test #7",
			vaultFullPath:        "DEVOPS/prod-cluster/k8s/secrets/harbor-cleaner",
			wantEngine:           "DEVOPS",
			wantPathWithinEngine: "prod-cluster/k8s/secrets/harbor-cleaner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, pathWithinEngine := ParseVaultPath(tt.vaultFullPath)
			assert.Equal(t, tt.wantEngine, engine)
			assert.Equal(t, tt.wantPathWithinEngine, pathWithinEngine)
		})
	}
}
