// Package s3backup discovers and matches S3 backups for Dokploy resources.
package s3backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/assurrussa/dokploymigrator/internal/model"
)

const manifestConfidence = "manifest"

// Config contains S3 connection settings.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
}

// Client lists backups from S3-compatible storage.
type Client struct {
	s3     *s3.Client
	bucket string
	prefix string
}

// Manifest is the optional metadata file expected near backup objects.
type Manifest struct {
	ResourceID   string             `json:"resourceId"`
	ResourceName string             `json:"resourceName"`
	ResourceType model.ResourceType `json:"resourceType"`
	CreatedAt    time.Time          `json:"createdAt"`
	Objects      []ManifestObject   `json:"objects"`
}

// ManifestObject describes one backup object in a manifest.
type ManifestObject struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
}

// New creates an S3 backup client.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		provider := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
		opts = append(opts, config.WithCredentialsProvider(provider))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})
	return &Client{s3: client, bucket: cfg.Bucket, prefix: strings.TrimPrefix(cfg.Prefix, "/")}, nil
}

// ListCandidates returns possible backup candidates for one resource.
func (c *Client) ListCandidates(ctx context.Context, resource model.Resource) ([]model.BackupCandidate, error) {
	prefix := c.prefix
	input := &s3.ListObjectsV2Input{Bucket: aws.String(c.bucket)}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}

	var candidates []model.BackupCandidate
	paginator := s3.NewListObjectsV2Paginator(c.s3, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list s3 backups: %w", err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == "" || strings.HasSuffix(key, "/") {
				continue
			}
			if isManifestKey(key) {
				manifestCandidates, err := c.candidatesFromManifest(ctx, key, resource)
				if err != nil {
					continue
				}
				candidates = append(candidates, manifestCandidates...)
				continue
			}
			if looksLikeBackup(key) && nameMatches(key, resource) {
				candidates = append(candidates, model.BackupCandidate{
					ResourceID:   resource.ID,
					ResourceName: resource.Name,
					ResourceType: resource.Type,
					Key:          key,
					ETag:         strings.Trim(aws.ToString(obj.ETag), `"`),
					Size:         aws.ToInt64(obj.Size),
					LastModified: aws.ToTime(obj.LastModified),
					Confidence:   "fallback-name",
				})
			}
		}
	}
	sortCandidates(candidates)
	return dedupeCandidates(candidates), nil
}

func (c *Client) candidatesFromManifest(
	ctx context.Context,
	key string,
	resource model.Resource,
) ([]model.BackupCandidate, error) {
	output, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("get manifest %s: %w", key, err)
	}
	defer output.Body.Close()
	body, err := io.ReadAll(io.LimitReader(output.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", key, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", key, err)
	}
	if !manifestMatches(manifest, resource) {
		return nil, nil
	}
	out := make([]model.BackupCandidate, 0, len(manifest.Objects))
	for _, obj := range manifest.Objects {
		if obj.Key == "" {
			continue
		}
		out = append(out, model.BackupCandidate{
			ResourceID:   manifest.ResourceID,
			ResourceName: manifest.ResourceName,
			ResourceType: manifest.ResourceType,
			Key:          obj.Key,
			Checksum:     obj.Checksum,
			Size:         obj.Size,
			LastModified: manifest.CreatedAt,
			Confidence:   manifestConfidence,
		})
	}
	return out, nil
}

func manifestMatches(manifest Manifest, resource model.Resource) bool {
	if manifest.ResourceID != "" && manifest.ResourceID == resource.ID {
		return true
	}
	if manifest.ResourceType != "" && manifest.ResourceType != resource.Type {
		return false
	}
	return strings.EqualFold(normalize(manifest.ResourceName), normalize(resource.Name))
}

func isManifestKey(key string) bool {
	base := strings.ToLower(path.Base(key))
	return base == "manifest.json" || strings.HasSuffix(base, ".manifest.json")
}

func looksLikeBackup(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, ".tar.gz") ||
		strings.HasSuffix(lower, ".tgz") ||
		strings.HasSuffix(lower, ".sql.gz") ||
		strings.HasSuffix(lower, ".dump") ||
		strings.HasSuffix(lower, ".archive")
}

func nameMatches(key string, resource model.Resource) bool {
	normalizedKey := normalize(key)
	return resource.ID != "" && strings.Contains(normalizedKey, normalize(resource.ID)) ||
		resource.Name != "" && strings.Contains(normalizedKey, normalize(resource.Name))
}

func normalize(value string) string {
	value = strings.ToLower(value)
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", ".", "-")
	return replacer.Replace(value)
}

func sortCandidates(candidates []model.BackupCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence != candidates[j].Confidence {
			return candidates[i].Confidence == manifestConfidence
		}
		return candidates[i].LastModified.After(candidates[j].LastModified)
	})
}

func dedupeCandidates(candidates []model.BackupCandidate) []model.BackupCandidate {
	seen := make(map[string]struct{}, len(candidates))
	out := make([]model.BackupCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.Key]; ok {
			continue
		}
		seen[candidate.Key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
