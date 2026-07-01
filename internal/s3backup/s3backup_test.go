package s3backup

import (
	"testing"
	"time"

	"github.com/assurrussa/dokploymigrator/internal/model"
)

func TestManifestMatches(t *testing.T) {
	resource := model.Resource{ID: "app-1", Name: "Billing API", Type: model.ResourceApplication}

	tests := []struct {
		name     string
		manifest Manifest
		want     bool
	}{
		{name: "id match wins", manifest: Manifest{ResourceID: "app-1", ResourceType: model.ResourceRedis}, want: true},
		{
			name:     "name and type match",
			manifest: Manifest{ResourceName: "billing_api", ResourceType: model.ResourceApplication},
			want:     true,
		},
		{name: "wrong type", manifest: Manifest{ResourceName: "billing-api", ResourceType: model.ResourceRedis}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := manifestMatches(tt.manifest, resource); got != tt.want {
				t.Fatalf("manifestMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortCandidates(t *testing.T) {
	now := time.Now()
	candidates := []model.BackupCandidate{
		{Key: "old.tar.gz", Confidence: "manifest", LastModified: now.Add(-time.Hour)},
		{Key: "fallback.tar.gz", Confidence: "fallback-name", LastModified: now.Add(time.Hour)},
		{Key: "new.tar.gz", Confidence: "manifest", LastModified: now},
	}
	sortCandidates(candidates)

	if candidates[0].Key != "new.tar.gz" || candidates[1].Key != "old.tar.gz" {
		t.Fatalf("unexpected order: %+v", candidates)
	}
}

func TestLooksLikeBackup(t *testing.T) {
	if !looksLikeBackup("foo/app.tar.gz") {
		t.Fatal("expected tar.gz backup")
	}
	if looksLikeBackup("foo/readme.txt") {
		t.Fatal("did not expect txt backup")
	}
}
