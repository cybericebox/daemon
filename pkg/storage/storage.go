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

// GetRange opens length bytes of an object starting at offset (an HTTP range read). The caller must Close the
// returned reader. Returns ErrObjectNotFound when the key is absent.
func (c *Client) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(offset, offset+length-1); err != nil {
		return nil, err
	}
	obj, err := c.mc.GetObject(ctx, c.bucket, key, opts)
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat forces the request so a missing key surfaces now.
	if _, err = obj.Stat(); err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	return obj, nil
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

// maxSingleCopyBytes is the largest object one S3 CopyObject request copies.
const maxSingleCopyBytes = 5 << 30

// Copy performs a server-side object copy within the bucket. An object over 5 GiB is copied in parts (multipart
// copy), still on the server.
func (c *Client) Copy(ctx context.Context, src, dst string) error {
	info, err := c.mc.StatObject(ctx, c.bucket, src, minio.StatObjectOptions{})
	if err != nil {
		return err
	}
	dstOpts := minio.CopyDestOptions{Bucket: c.bucket, Object: dst}
	srcOpts := minio.CopySrcOptions{Bucket: c.bucket, Object: src}
	if info.Size > maxSingleCopyBytes {
		_, err = c.mc.ComposeObject(ctx, dstOpts, srcOpts)
		return err
	}
	_, err = c.mc.CopyObject(ctx, dstOpts, srcOpts)
	return err
}
