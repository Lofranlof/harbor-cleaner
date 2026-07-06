package domain

import "slices"

// noneSentinel is the config value meaning "preserve nothing via this rule" -
// callers pass it explicitly rather than an empty list to make "I really mean
// nothing" distinguishable from "I forgot to configure this".
const noneSentinel = "none"

// PreserveByDigestSet marks every artifact whose digest is in liveDigests as
// preserved. Used to keep images that are actually deployed somewhere (e.g.
// currently referenced by a Kubernetes workload).
func PreserveByDigestSet(projects []*Project, liveDigests map[string]struct{}) int {
	preserved := 0
	for _, project := range projects {
		for _, repo := range project.Repos {
			for _, art := range repo.Artifacts {
				if _, ok := liveDigests[art.Digest]; ok && !art.Preserve {
					art.Preserve = true
					preserved++
				}
			}
		}
	}
	return preserved
}

// PreserveByMaxAge marks every artifact younger than maxAgeHours as preserved.
func PreserveByMaxAge(projects []*Project, maxAgeHours int) int {
	preserved := 0
	for _, project := range projects {
		for _, repo := range project.Repos {
			for _, art := range repo.Artifacts {
				if art.AgeHours <= maxAgeHours && !art.Preserve {
					art.Preserve = true
					preserved++
				}
			}
		}
	}
	return preserved
}

// PreserveByTopN marks the topN newest artifacts within each repository as
// preserved (AgePosition is 1-based, so AgePosition <= topN). Repositories with
// fewer than topN artifacts have all of their artifacts preserved.
//
// Repository.SortArtifactsByAgeAndCalculatePosition must have been called first
// so AgePosition is populated.
func PreserveByTopN(projects []*Project, topN int) int {
	preserved := 0
	for _, project := range projects {
		for _, repo := range project.Repos {
			for _, art := range repo.Artifacts {
				if art.AgePosition <= topN && !art.Preserve {
					art.Preserve = true
					preserved++
				}
			}
		}
	}
	return preserved
}

// PreserveByAllowListProjects preserves every artifact belonging to a project
// whose name is in allowedProjects. Passing []string{"none"} is a no-op.
func PreserveByAllowListProjects(projects []*Project, allowedProjects []string) int {
	if slices.Contains(allowedProjects, noneSentinel) {
		return 0
	}
	preserved := 0
	for _, project := range projects {
		if !slices.Contains(allowedProjects, project.Name) {
			continue
		}
		for _, repo := range project.Repos {
			for _, art := range repo.Artifacts {
				if !art.Preserve {
					art.Preserve = true
					preserved++
				}
			}
		}
	}
	return preserved
}

// PreserveByAllowListRepos preserves every artifact belonging to a repository
// whose full name (project prefix included, matching the Harbor API's naming)
// is in allowedRepos. Passing []string{"none"} is a no-op.
func PreserveByAllowListRepos(projects []*Project, allowedRepos []string) int {
	if slices.Contains(allowedRepos, noneSentinel) {
		return 0
	}
	preserved := 0
	for _, project := range projects {
		for _, repo := range project.Repos {
			if !slices.Contains(allowedRepos, repo.Name) {
				continue
			}
			for _, art := range repo.Artifacts {
				if !art.Preserve {
					art.Preserve = true
					preserved++
				}
			}
		}
	}
	return preserved
}
