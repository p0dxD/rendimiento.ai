// Package logarchive moves the step logs of finished CI runs out of
// Postgres into object storage (MinIO or any S3), gzip-compressed, and
// reads them back for the UI. A lifecycle rule on the bucket deletes them
// after the retention period, so storage stays bounded on its own.
package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Prefix is where step logs live in the bucket: runs/<app>/<run>/<step>.log.gz.
const Prefix = "runs/"

// ErrExpired means an archived log was deleted by the retention rule.
var ErrExpired = errors.New("the log has expired")

// Objects stores and fetches whole objects (MinIO in production).
type Objects interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error) // ErrExpired when missing
}

// Store is what the archiver needs from the database.
type Store interface {
	UnarchivedSteps(ctx context.Context, before time.Time, limit int) ([]store.ArchivableStep, error)
	MarkArchived(ctx context.Context, runID int64, stepID, ref string, bytes int) (bool, error)
}

// Archive moves finished runs' step logs to Objects every Every.
type Archive struct {
	Objects       Objects
	Store         Store
	Log           *slog.Logger
	Every         time.Duration // between sweeps (default 5m)
	Settle        time.Duration // a run must have finished this long ago (default 2m)
	RetentionDays int           // for messages; the bucket's rule does the deleting
}

// Run sweeps until ctx is done.
func (a *Archive) Run(ctx context.Context) {
	every := a.Every
	if every <= 0 {
		every = 5 * time.Minute
	}
	for {
		n, err := a.Sweep(ctx)
		switch {
		case err != nil:
			a.Log.Warn("log archive: sweep failed; will retry", "err", err)
		case n > 0:
			a.Log.Info("log archive: archived step logs", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Sweep archives the logs of finished runs, in batches, until none are
// left or one fails. It returns how many it archived. A batch in which no
// log could be marked archived stops the sweep with an error, so a bug or
// a race can never make it upload the same logs over and over.
func (a *Archive) Sweep(ctx context.Context) (int, error) {
	settle := a.Settle
	if settle <= 0 {
		settle = 2 * time.Minute
	}
	total := 0
	for {
		steps, err := a.Store.UnarchivedSteps(ctx, time.Now().Add(-settle), 100)
		if err != nil || len(steps) == 0 {
			return total, err
		}
		progress := 0
		for _, st := range steps {
			key := Key(st.App, st.RunID, st.StepID)
			data, err := compress(st.Log)
			if err != nil {
				return total, err
			}
			if err := a.Objects.Put(ctx, key, data); err != nil {
				return total, fmt.Errorf("store %s: %w", key, err)
			}
			marked, err := a.Store.MarkArchived(ctx, st.RunID, st.StepID, key, len(st.Log))
			if err != nil {
				return total, err
			}
			if marked {
				progress++
				total++
			}
		}
		if progress == 0 {
			return total, fmt.Errorf("%d logs were uploaded but none could be marked archived; stopping this sweep", len(steps))
		}
	}
}

var unsafeKey = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// Key is a step log's object key.
func Key(app string, runID int64, stepID string) string {
	return fmt.Sprintf("%s%s/%d/%s.log.gz", Prefix, unsafeKey.ReplaceAllString(app, "_"), runID, unsafeKey.ReplaceAllString(stepID, "_"))
}

// Read returns an archived log's text.
func (a *Archive) Read(ctx context.Context, key string) (string, error) {
	data, err := a.Objects.Get(ctx, key)
	if err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	text, err := io.ReadAll(zr)
	return string(text), err
}

// compress gzips a log as small as it goes.
func compress(s string) ([]byte, error) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := io.WriteString(zw, s); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- MinIO ----

// MinIO stores objects in one bucket of a MinIO (or other S3) server.
type MinIO struct {
	Client *minio.Client
	Bucket string
}

// NewMinIO connects to endpoint (host:port) with an access key.
func NewMinIO(endpoint, accessKey, secretKey, bucket string, secure bool) (*MinIO, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure})
	if err != nil {
		return nil, err
	}
	return &MinIO{Client: c, Bucket: bucket}, nil
}

// Put uploads one object.
func (m *MinIO) Put(ctx context.Context, key string, data []byte) error {
	_, err := m.Client.PutObject(ctx, m.Bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/gzip"})
	return err
}

// Get downloads one object; ErrExpired when it no longer exists.
func (m *MinIO) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := m.Client.GetObject(ctx, m.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil, ErrExpired
	}
	return data, err
}

// EnsureRetention checks the bucket exists and sets its rule: logs under
// Prefix are deleted days after they were archived.
func (m *MinIO) EnsureRetention(ctx context.Context, days int) error {
	ok, err := m.Client.BucketExists(ctx, m.Bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q does not exist", m.Bucket)
	}
	cfg := lifecycle.NewConfiguration()
	cfg.Rules = []lifecycle.Rule{{
		ID:         "rendimiento-log-retention",
		Status:     "Enabled",
		RuleFilter: lifecycle.Filter{Prefix: Prefix},
		Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(days)},
	}}
	return m.Client.SetBucketLifecycle(ctx, m.Bucket, cfg)
}
