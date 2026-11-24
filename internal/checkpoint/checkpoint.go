package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aashiruu/spot-evict/internal/workload"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Envelope wraps workload state with fencing metadata and an integrity digest.
type Envelope struct {
	Version      int            `json:"version"`
	RunID        string         `json:"run_id"`
	FencingToken int64          `json:"fencing_token"`
	WorkerID     string         `json:"worker_id"`
	CreatedAt    string         `json:"created_at"`
	State        workload.State `json:"state"`
	PadBase64    string         `json:"pad_base64,omitempty"`
	PayloadSHA   string         `json:"payload_sha256"`
}

// WriteMetrics captures the cost and latency of a single checkpoint save.
type WriteMetrics struct {
	Key          string        `json:"key"`
	Bytes        int           `json:"bytes"`
	Duration     time.Duration `json:"duration"`
	S3PutCalls   int           `json:"s3_put_calls"`
	S3DelCalls   int           `json:"s3_delete_calls"`
	TotalS3Calls int           `json:"total_s3_calls"`
}

// Store abstracts checkpoint persistence (S3 or local filesystem for unit tests).
type Store interface {
	Save(ctx context.Context, runID string, fencingToken int64, workerID string, state workload.State, padBytes int, keepLast int) (WriteMetrics, error)
	LoadLatest(ctx context.Context, runID string) (*Envelope, []string, error)
}

// EncodeEnvelope serializes state (plus optional deterministic padding) and computes PayloadSHA.
func EncodeEnvelope(runID string, fencingToken int64, workerID string, state workload.State, padBytes int) ([]byte, error) {
	env := Envelope{
		Version:      1,
		RunID:        runID,
		FencingToken: fencingToken,
		WorkerID:     workerID,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		State:        state,
	}
	if padBytes > 0 {
		raw := make([]byte, padBytes)
		for i := range raw {
			raw[i] = byte((int(state.NextChunk) + i) & 0xff)
		}
		env.PadBase64 = base64.StdEncoding.EncodeToString(raw)
	}
	env.PayloadSHA = computeEnvelopeDigest(env)
	return json.Marshal(env)
}

// DecodeAndVerify parses raw JSON bytes and verifies the embedded SHA-256 digest.
func DecodeAndVerify(raw []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("json decode failed: %w", err)
	}
	if env.Version != 1 || env.PayloadSHA == "" {
		return nil, errors.New("missing version or payload_sha256")
	}
	expected := computeEnvelopeDigest(env)
	if env.PayloadSHA != expected {
		return nil, fmt.Errorf("checksum mismatch: got %s, expected %s", env.PayloadSHA, expected)
	}
	return &env, nil
}

func computeEnvelopeDigest(env Envelope) string {
	copyEnv := env
	copyEnv.PayloadSHA = ""
	canonical, _ := json.Marshal(copyEnv)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func objectName(nextChunk int64, fencingToken int64) string {
	return fmt.Sprintf("ckpt-%012d-f%012d.json", nextChunk, fencingToken)
}

// S3Store persists checkpoints in Amazon S3 using single-object PutObject with SHA-256 verification.
type S3Store struct {
	Client *s3.Client
	Bucket string
	Prefix string
}

func (s *S3Store) keyFor(runID, name string) string {
	base := strings.Trim(s.Prefix, "/")
	if base == "" {
		return fmt.Sprintf("checkpoints/%s/%s", runID, name)
	}
	return fmt.Sprintf("%s/checkpoints/%s/%s", base, runID, name)
}

func (s *S3Store) Save(ctx context.Context, runID string, fencingToken int64, workerID string, state workload.State, padBytes int, keepLast int) (WriteMetrics, error) {
	start := time.Now()
	body, err := EncodeEnvelope(runID, fencingToken, workerID, state, padBytes)
	if err != nil {
		return WriteMetrics{}, err
	}

	sum := sha256.Sum256(body)
	b64SHA := base64.StdEncoding.EncodeToString(sum[:])
	key := s.keyFor(runID, objectName(state.NextChunk, fencingToken))

	_, err = s.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(s.Bucket),
		Key:               aws.String(key),
		Body:              bytes.NewReader(body),
		ContentType:       aws.String("application/json"),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
		ChecksumSHA256:    aws.String(b64SHA),
	})
	if err != nil {
		return WriteMetrics{}, fmt.Errorf("s3 PutObject %s: %w", key, err)
	}

	delCalls := 0
	if keepLast > 0 {
		prefix := s.keyFor(runID, "ckpt-")
		listOut, err := s.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.Bucket),
			Prefix: aws.String(prefix),
		})
		if err == nil && len(listOut.Contents) > keepLast {
			keys := make([]string, 0, len(listOut.Contents))
			for _, obj := range listOut.Contents {
				keys = append(keys, aws.ToString(obj.Key))
			}
			sort.Strings(keys)
			for _, oldKey := range keys[:len(keys)-keepLast] {
				_, _ = s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(s.Bucket),
					Key:    aws.String(oldKey),
				})
				delCalls++
			}
		}
	}

	return WriteMetrics{
		Key:          key,
		Bytes:        len(body),
		Duration:     time.Since(start),
		S3PutCalls:   1,
		S3DelCalls:   delCalls,
		TotalS3Calls: 1 + delCalls,
	}, nil
}

// LoadLatest lists checkpoints newest-first, verifies checksums, and falls back to older objects on corruption.
func (s *S3Store) LoadLatest(ctx context.Context, runID string) (*Envelope, []string, error) {
	prefix := s.keyFor(runID, "ckpt-")
	listOut, err := s.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.Bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("s3 ListObjectsV2: %w", err)
	}
	if len(listOut.Contents) == 0 {
		return nil, nil, nil
	}

	keys := make([]string, 0, len(listOut.Contents))
	for _, obj := range listOut.Contents {
		keys = append(keys, aws.ToString(obj.Key))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))

	var warnings []string
	for _, k := range keys {
		getOut, err := s.Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket:       aws.String(s.Bucket),
			Key:          aws.String(k),
			ChecksumMode: types.ChecksumModeEnabled,
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("rejected %s: get error: %v", k, err))
			continue
		}
		raw, readErr := io.ReadAll(getOut.Body)
		_ = getOut.Body.Close()
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("rejected %s: read error: %v", k, readErr))
			continue
		}
		env, verifyErr := DecodeAndVerify(raw)
		if verifyErr != nil {
			warnings = append(warnings, fmt.Sprintf("rejected %s: %v", k, verifyErr))
			continue
		}
		return env, warnings, nil
	}
	return nil, warnings, fmt.Errorf("all %d candidate checkpoints failed verification", len(keys))
}

// FileStore is a local filesystem implementation used for fast offline unit tests.
type FileStore struct {
	Dir string
}

func (f *FileStore) Save(_ context.Context, runID string, fencingToken int64, workerID string, state workload.State, padBytes int, keepLast int) (WriteMetrics, error) {
	start := time.Now()
	body, err := EncodeEnvelope(runID, fencingToken, workerID, state, padBytes)
	if err != nil {
		return WriteMetrics{}, err
	}
	dir := filepath.Join(f.Dir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return WriteMetrics{}, err
	}
	name := objectName(state.NextChunk, fencingToken)
	full := filepath.Join(dir, name)
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return WriteMetrics{}, err
	}
	if err := os.Rename(tmp, full); err != nil {
		return WriteMetrics{}, err
	}
	if keepLast > 0 {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "ckpt-") && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		if len(names) > keepLast {
			for _, old := range names[:len(names)-keepLast] {
				_ = os.Remove(filepath.Join(dir, old))
			}
		}
	}
	return WriteMetrics{
		Key:          full,
		Bytes:        len(body),
		Duration:     time.Since(start),
		S3PutCalls:   1,
		TotalS3Calls: 1,
	}, nil
}

func (f *FileStore) LoadLatest(_ context.Context, runID string) (*Envelope, []string, error) {
	dir := filepath.Join(f.Dir, runID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ckpt-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil, nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var warnings []string
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("rejected %s: %v", n, err))
			continue
		}
		env, err := DecodeAndVerify(raw)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("rejected %s: %v", n, err))
			continue
		}
		return env, warnings, nil
	}
	return nil, warnings, fmt.Errorf("all %d candidate checkpoints failed verification", len(names))
}
