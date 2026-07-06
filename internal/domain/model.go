// Package domain holds the core data model and retention rules for the registry
// cleaner. It has zero dependencies on any external system (Harbor, Vault, K8s) -
// everything here is plain data and pure functions, testable without a network.
package domain

import (
	"sort"
	"time"
)

// Tag is a single tag pointing at an artifact.
type Tag struct {
	Name     string
	PushTime time.Time
}

// Artifact is a single content-addressable image (identified by digest) that may
// have zero or more tags pointing at it.
type Artifact struct {
	Repo      *Repository
	Digest    string
	Tags      []Tag
	SizeBytes int64

	// PushTime is the latest push time among the artifact itself and all its tags -
	// an artifact's own push time is set when it's first uploaded, but re-tagging
	// an existing digest bumps the tag's push time without changing the artifact's.
	PushTime time.Time

	AgeHours    int  // age of PushTime in hours, relative to time.Now() at construction
	AgePosition int  // 1-based rank by freshness within its repository (1 = newest)
	Preserve    bool // set by retention rules; artifacts left false are eligible for deletion
}

// NewArtifact builds an Artifact, computing PushTime/AgeHours from the artifact's
// own push time and the push times of its tags.
func NewArtifact(digest string, sizeBytes int64, artifactPushTime time.Time, tags []Tag) *Artifact {
	latest := artifactPushTime
	for _, tag := range tags {
		if tag.PushTime.After(latest) {
			latest = tag.PushTime
		}
	}
	return &Artifact{
		Digest:    digest,
		Tags:      tags,
		SizeBytes: sizeBytes,
		PushTime:  latest,
		AgeHours:  calculateAgeInHours(latest),
	}
}

func calculateAgeInHours(t time.Time) int {
	return int(time.Since(t).Hours())
}

// Repository is a Harbor repository within a project.
type Repository struct {
	Project *Project

	// Name is the repository's full name as returned by the Harbor API, i.e.
	// "<project>/<repo>".
	Name string
	// NameWithinProject strips the project prefix off Name - Harbor's own API
	// calls expect repository names without their project prefix.
	NameWithinProject string

	Artifacts []*Artifact
}

// SortArtifactsByAgeAndCalculatePosition orders Artifacts newest-first and assigns
// each one its AgePosition (1 = newest).
func (r *Repository) SortArtifactsByAgeAndCalculatePosition() {
	sort.Slice(r.Artifacts, func(i, j int) bool {
		return r.Artifacts[i].AgeHours < r.Artifacts[j].AgeHours
	})
	for i, a := range r.Artifacts {
		a.AgePosition = i + 1
	}
}

// Project is a Harbor project (a namespace grouping repositories).
type Project struct {
	Name  string
	Repos []*Repository
}
