package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// dataCustomTime is a small convenience constructor for data.CustomTime,
// used throughout the test fixtures below.
func dataCustomTime(t time.Time) data.CustomTime {
	return data.CustomTime{Time: t}
}

// newDiscardLogger builds a *slog.Logger that throws its output away, so
// tests can construct an *imagePipeline directly without spamming stdout.
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// This file contains hand-rolled test doubles for the interfaces the pipeline
// package depends on (Repository, data.Indexer, crypt.Cryptor,
// storage.ObjectStorage, storage.ReadSeekCloser). Each mock is a struct of
// optional function fields: tests set only the behaviors they care about, and
// unset methods fall back to a harmless zero-value default so table-driven
// tests can stay terse.

// ------------------------------------------------------------------
// Repository mock
// ------------------------------------------------------------------

type mockRepository struct {
	mu sync.Mutex

	findAllAlbumsFn        func() ([]api.AlbumRecord, error)
	findImageFn            func(slugIndex string) (*api.ImageRecord, error)
	findImageAlbumsFn      func(imageId string) ([]api.AlbumRecord, error)
	insertAlbumFn          func(record api.AlbumRecord) error
	insertAlbumImageXrefFn func(xref api.AlbumImageXref) error
	updateImageFn          func(record api.ImageRecord) error

	insertAlbumCalls     []api.AlbumRecord
	insertAlbumXrefCalls []api.AlbumImageXref
	updateImageCalls     []api.ImageRecord
	findImageAlbumsCalls []string
	findImageCalls       []string
	findAllAlbumsCalls   int
}

var _ Repository = (*mockRepository)(nil)

func (m *mockRepository) FindAllAlbums() ([]api.AlbumRecord, error) {
	m.mu.Lock()
	m.findAllAlbumsCalls++
	m.mu.Unlock()

	if m.findAllAlbumsFn != nil {
		return m.findAllAlbumsFn()
	}
	return nil, nil
}

func (m *mockRepository) FindImage(slugIndex string) (*api.ImageRecord, error) {
	m.mu.Lock()
	m.findImageCalls = append(m.findImageCalls, slugIndex)
	m.mu.Unlock()

	if m.findImageFn != nil {
		return m.findImageFn(slugIndex)
	}
	return nil, fmt.Errorf("mockRepository: FindImage not stubbed")
}

func (m *mockRepository) FindImageAlbums(imageId string) ([]api.AlbumRecord, error) {
	m.mu.Lock()
	m.findImageAlbumsCalls = append(m.findImageAlbumsCalls, imageId)
	m.mu.Unlock()

	if m.findImageAlbumsFn != nil {
		return m.findImageAlbumsFn(imageId)
	}
	return nil, nil
}

func (m *mockRepository) InsertAlbum(record api.AlbumRecord) error {
	m.mu.Lock()
	m.insertAlbumCalls = append(m.insertAlbumCalls, record)
	m.mu.Unlock()

	if m.insertAlbumFn != nil {
		return m.insertAlbumFn(record)
	}
	return nil
}

func (m *mockRepository) InsertAlbumImageXref(xref api.AlbumImageXref) error {
	m.mu.Lock()
	m.insertAlbumXrefCalls = append(m.insertAlbumXrefCalls, xref)
	m.mu.Unlock()

	if m.insertAlbumImageXrefFn != nil {
		return m.insertAlbumImageXrefFn(xref)
	}
	return nil
}

// updateImageCallCount and insertAlbumCallCount are lock-guarded accessors for
// tests that poll a background goroutine's progress (e.g. waiting for a Queue
// loop to finish processing an item); reading the slices directly from
// outside the mock would race with the goroutine appending to them.
func (m *mockRepository) updateImageCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.updateImageCalls)
}

func (m *mockRepository) insertAlbumCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.insertAlbumCalls)
}

func (m *mockRepository) UpdateImage(record api.ImageRecord) error {
	m.mu.Lock()
	m.updateImageCalls = append(m.updateImageCalls, record)
	m.mu.Unlock()

	if m.updateImageFn != nil {
		return m.updateImageFn(record)
	}
	return nil
}

// ------------------------------------------------------------------
// data.Indexer mock
// ------------------------------------------------------------------

type mockIndexer struct {
	mu sync.Mutex

	obtainBlindIndexFn func(s string) (string, error)

	obtainBlindIndexCalls []string
}

func (m *mockIndexer) ObtainBlindIndex(s string) (string, error) {
	m.mu.Lock()
	m.obtainBlindIndexCalls = append(m.obtainBlindIndexCalls, s)
	m.mu.Unlock()

	if m.obtainBlindIndexFn != nil {
		return m.obtainBlindIndexFn(s)
	}
	return "index-" + s, nil
}

// ------------------------------------------------------------------
// crypt.Cryptor mock -> identity encrypt/decrypt by default (no-op)
// ------------------------------------------------------------------

type mockCryptor struct {
	encryptAlbumRecordFn func(a *api.AlbumRecord) error
	encryptAlbumFn       func(a *api.Album) error
	decryptAlbumRecordFn func(a *api.AlbumRecord) error
	decryptAlbumFn       func(a *api.Album) error

	decryptAlbumImageFn func(r *api.AlbumImageRecord) error

	encryptImageRecordFn func(i *api.ImageRecord) error
	encryptImageDataFn   func(i *api.ImageData) error
	decryptImageDataFn   func(i *api.ImageData) error
	decryptImageRecordFn func(i *api.ImageRecord) error
}

func (m *mockCryptor) EncryptAlbumRecord(a *api.AlbumRecord) error {
	if m.encryptAlbumRecordFn != nil {
		return m.encryptAlbumRecordFn(a)
	}
	return nil
}

func (m *mockCryptor) EncryptAlbum(a *api.Album) error {
	if m.encryptAlbumFn != nil {
		return m.encryptAlbumFn(a)
	}
	return nil
}

func (m *mockCryptor) DecryptAlbumRecord(a *api.AlbumRecord) error {
	if m.decryptAlbumRecordFn != nil {
		return m.decryptAlbumRecordFn(a)
	}
	return nil
}

func (m *mockCryptor) DecryptAlbum(a *api.Album) error {
	if m.decryptAlbumFn != nil {
		return m.decryptAlbumFn(a)
	}
	return nil
}

func (m *mockCryptor) DecryptAlbumImage(r *api.AlbumImageRecord) error {
	if m.decryptAlbumImageFn != nil {
		return m.decryptAlbumImageFn(r)
	}
	return nil
}

func (m *mockCryptor) EncryptImageRecord(i *api.ImageRecord) error {
	if m.encryptImageRecordFn != nil {
		return m.encryptImageRecordFn(i)
	}
	return nil
}

func (m *mockCryptor) EncryptImageData(i *api.ImageData) error {
	if m.encryptImageDataFn != nil {
		return m.encryptImageDataFn(i)
	}
	return nil
}

func (m *mockCryptor) DecryptImageData(i *api.ImageData) error {
	if m.decryptImageDataFn != nil {
		return m.decryptImageDataFn(i)
	}
	return nil
}

func (m *mockCryptor) DecryptImageRecord(i *api.ImageRecord) error {
	if m.decryptImageRecordFn != nil {
		return m.decryptImageRecordFn(i)
	}
	return nil
}

// ------------------------------------------------------------------
// storage.ObjectStorage mock
// ------------------------------------------------------------------

type mockObjectStorage struct {
	mu sync.Mutex

	listObjectsFn        func(ctx context.Context, prefix string) ([]string, error)
	getSignedUrlFn       func(ctx context.Context, objectKey string) (*url.URL, error)
	getPreSignedPutUrlFn func(ctx context.Context, objectKey string) (*url.URL, error)
	moveObjectFn         func(ctx context.Context, src, dst string) error
	withObjectFn         func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error
	putObjectFn          func(ctx context.Context, key string, data []byte, contentType string) error
	deleteObjectsFn      func(ctx context.Context, keys []string) error
	deleteObjectFn       func(ctx context.Context, key string) error

	putObjectCalls    []string
	moveObjectCalls   [][2]string
	listObjectsCalls  []string
	deleteObjectsCall [][]string
}

var _ storage.ObjectStorage = (*mockObjectStorage)(nil)

func (m *mockObjectStorage) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	m.listObjectsCalls = append(m.listObjectsCalls, prefix)
	m.mu.Unlock()

	if m.listObjectsFn != nil {
		return m.listObjectsFn(ctx, prefix)
	}
	return nil, nil
}

func (m *mockObjectStorage) GetSignedUrl(ctx context.Context, objectKey string) (*url.URL, error) {
	if m.getSignedUrlFn != nil {
		return m.getSignedUrlFn(ctx, objectKey)
	}
	return nil, nil
}

func (m *mockObjectStorage) GetPreSignedPutUrl(ctx context.Context, objectKey string) (*url.URL, error) {
	if m.getPreSignedPutUrlFn != nil {
		return m.getPreSignedPutUrlFn(ctx, objectKey)
	}
	return nil, nil
}

func (m *mockObjectStorage) MoveObject(ctx context.Context, src, dst string) error {
	m.mu.Lock()
	m.moveObjectCalls = append(m.moveObjectCalls, [2]string{src, dst})
	m.mu.Unlock()

	if m.moveObjectFn != nil {
		return m.moveObjectFn(ctx, src, dst)
	}
	return nil
}

func (m *mockObjectStorage) WithObject(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
	if m.withObjectFn != nil {
		return m.withObjectFn(ctx, key, fn)
	}
	return fn(newFakeReadSeekCloser(nil))
}

func (m *mockObjectStorage) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	m.putObjectCalls = append(m.putObjectCalls, key)
	m.mu.Unlock()

	if m.putObjectFn != nil {
		return m.putObjectFn(ctx, key, data, contentType)
	}
	return nil
}

// deleteObjectsCallCount is a lock-guarded accessor; see updateImageCallCount
// for why polling tests must not read the raw slice directly.
func (m *mockObjectStorage) deleteObjectsCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.deleteObjectsCall)
}

func (m *mockObjectStorage) DeleteObjects(ctx context.Context, keys []string) error {
	m.mu.Lock()
	m.deleteObjectsCall = append(m.deleteObjectsCall, keys)
	m.mu.Unlock()

	if m.deleteObjectsFn != nil {
		return m.deleteObjectsFn(ctx, keys)
	}
	return nil
}

func (m *mockObjectStorage) DeleteObject(ctx context.Context, key string) error {
	if m.deleteObjectFn != nil {
		return m.deleteObjectFn(ctx, key)
	}
	return nil
}

// ------------------------------------------------------------------
// storage.ReadSeekCloser fakes
// ------------------------------------------------------------------

// fakeReadSeekCloser wraps an in-memory byte slice to satisfy
// storage.ReadSeekCloser for tests.
type fakeReadSeekCloser struct {
	*bytes.Reader
	closed bool
}

func newFakeReadSeekCloser(data []byte) *fakeReadSeekCloser {
	return &fakeReadSeekCloser{Reader: bytes.NewReader(data)}
}

func (f *fakeReadSeekCloser) Close() error {
	f.closed = true
	return nil
}

// seekErrReadSeekCloser always fails on Seek, used to exercise ReadExif's
// error path when rewinding the reader fails.
type seekErrReadSeekCloser struct{}

func (seekErrReadSeekCloser) Read(p []byte) (int, error) { return 0, fmt.Errorf("read not supported") }
func (seekErrReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	return 0, fmt.Errorf("seek failed")
}
func (seekErrReadSeekCloser) Close() error { return nil }
