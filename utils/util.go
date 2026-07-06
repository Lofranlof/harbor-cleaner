package utils

import (
	"strings"
)

// Input raw repository name and get repository name within project
// Raw repository name consists of ProjectName + RepositoryName within that project.
// API calls to harbor usually expect a RepositoryName within specific project
// And thats why this function exists
// Basically strips everything before the first "/" symbol and returns the result
func GetRepoNameWithinProject(repoNameWithProject string) string {
	repoNameSlice := strings.Split(repoNameWithProject, "/")
	return strings.Join(repoNameSlice[1:], "/")
}

// Input raw repository name and get repository name within project
// Raw repository name consists of ProjectName + RepositoryName within that project.
// Will extract the first substring before "/" symbol which is the project name of a repo
func GetProjectNameOfRepository(repoNameWithProject string) string {
	repoNameSlice := strings.Split(repoNameWithProject, "/")
	return repoNameSlice[0]
}

// Parses full vault path to engine and path within engine
func ParseVaultPath(fullPath string) (engine string, pathWithinEngine string) {
	fullPathSlice := strings.Split(fullPath, "/")
	engine = fullPathSlice[0]
	pathWithinEngine = strings.Join(fullPathSlice[1:], "/")
	return engine, pathWithinEngine
}
