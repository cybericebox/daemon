// Package storage is a thin S3/MinIO object-store client.
package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrObjectNotFound is returned by Get/Remove when the key does not exist.
var ErrObjectNotFound = errors.New("storage: object not found")

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

type Client struct {
	mc     *minio.Client
	bucket string
}

// New builds the client and ensures the bucket exists.
func New(cfg Config) (*Client, error) {
	mc, err := minio.New(
		cfg.Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
			Secure: cfg.UseSSL,
			Region: cfg.Region,
		},
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exists, err := mc.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err = mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, err
		}
	}

	return &Client{mc: mc, bucket: cfg.Bucket}, nil
}

// Put streams an object into storage. size may be -1 if unknown.
func (c *Client) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get opens an object stream and reports its content type. The caller must Close
// the returned reader. Returns ErrObjectNotFound when the key is absent.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	// GetObject is lazy; Stat forces the request so a missing key surfaces now.
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close() // keep the Stat error: closing must not replace it
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return nil, "", ErrObjectNotFound
		}
		return nil, "", err
	}
	return obj, info.ContentType, nil
}

// Remove deletes an object. A missing key is not an error.
func (c *Client) Remove(ctx context.Context, key string) error {
	return c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{ForceDelete: true})
}

// Stat reports whether the key exists (no error for a clean miss).
func (c *Client) Stat(ctx context.Context, key string) (bool, error) {
	_, err := c.mc.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Copy performs a server-side object copy within the bucket.
func (c *Client) Copy(ctx context.Context, src, dst string) error {
	_, err := c.mc.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: c.bucket, Object: dst},
		minio.CopySrcOptions{Bucket: c.bucket, Object: src},
	)
	return err
}
