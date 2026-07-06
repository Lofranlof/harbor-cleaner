package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewArtifactPushTime(t *testing.T) {
	tests := []struct {
		name             string
		artifactPushTime time.Time
		tags             []Tag
		expected         time.Time
	}{
		{
			name:             "ArtifactWithNoTags",
			artifactPushTime: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			tags:             nil,
			expected:         time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name:             "ArtifactWithLaterTagPushTimes",
			artifactPushTime: time.Date(2024, 12, 12, 16, 37, 0, 0, time.UTC),
			tags: []Tag{
				{PushTime: time.Date(2024, 12, 12, 16, 37, 0, 0, time.UTC)},
				{PushTime: time.Date(2024, 12, 16, 16, 24, 0, 0, time.UTC)},
				{PushTime: time.Date(2024, 12, 16, 17, 58, 0, 0, time.UTC)},
			},
			expected: time.Date(2024, 12, 16, 17, 58, 0, 0, time.UTC),
		},
		{
			// невозможно на практике (артефакту присваивается push time самого раннего тега),
			// но правило "берём максимум" должно работать корректно в любом случае
			name:             "ArtifactWithEarlierTagPushTimes",
			artifactPushTime: time.Date(2025, 1, 4, 12, 0, 0, 0, time.UTC),
			tags: []Tag{
				{PushTime: time.Date(2025, 1, 2, 14, 0, 0, 0, time.UTC)},
				{PushTime: time.Date(2025, 1, 3, 16, 0, 0, 0, time.UTC)},
			},
			expected: time.Date(2025, 1, 4, 12, 0, 0, 0, time.UTC),
		},
		{
			name:             "ArtifactWithNoPushTimeAndNoTags",
			artifactPushTime: time.Time{},
			tags:             nil,
			expected:         time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			art := NewArtifact("sha256:deadbeef", 1024, tt.artifactPushTime, tt.tags)
			assert.Equal(t, tt.expected, art.PushTime)
		})
	}
}

func TestRepositorySortArtifactsByAgeAndCalculatePosition(t *testing.T) {
	tests := []struct {
		name                   string
		artifacts              []*Artifact
		expectedArtifactsOrder []string
	}{
		{
			name: "ThreeArtifactsDistinctAges",
			artifacts: []*Artifact{
				{Digest: "testArt1", AgeHours: 500},
				{Digest: "testArt2", AgeHours: 100},
				{Digest: "testArt3", AgeHours: 200},
			},
			expectedArtifactsOrder: []string{"testArt2", "testArt3", "testArt1"},
		},
		{
			name: "TiedAgesKeepRelativeOrder",
			artifacts: []*Artifact{
				{Digest: "testArt1", AgeHours: 500},
				{Digest: "testArt2", AgeHours: 10},
				{Digest: "testArt3", AgeHours: 10},
			},
			expectedArtifactsOrder: []string{"testArt2", "testArt3", "testArt1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &Repository{Artifacts: tt.artifacts}
			repo.SortArtifactsByAgeAndCalculatePosition()
			for i, art := range repo.Artifacts {
				assert.Equal(t, tt.expectedArtifactsOrder[i], art.Digest)
				assert.Equal(t, i+1, art.AgePosition)
			}
		})
	}
}
