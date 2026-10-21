package testhelpers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
)

// MemoryStorage substitutes only object bytes. Integration tests still use the
// real media use case, source rows, ownership checks and reference transactions.
type MemoryStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func NewMemoryStorage() *MemoryStorage { return &MemoryStorage{objects: map[string][]byte{}} }
func (s *MemoryStorage) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}
func (s *MemoryStorage) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, "", errors.New("object missing")
	}
	return io.NopCloser(bytes.NewReader(b)), "", nil
}
func (s *MemoryStorage) GetRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok || offset < 0 || offset > int64(len(b)) {
		return nil, errors.New("object range missing")
	}
	return io.NopCloser(bytes.NewReader(b[offset:min(offset+length, int64(len(b)))])), nil
}
func (s *MemoryStorage) Remove(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}
func (s *MemoryStorage) Stat(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}
func (s *MemoryStorage) Copy(_ context.Context, src, dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[src]
	if !ok {
		return errors.New("object missing")
	}
	s.objects[dst] = append([]byte(nil), b...)
	return nil
}
