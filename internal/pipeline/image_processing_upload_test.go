package pipeline

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"

	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// baseImageRecord is a valid api.ImageRecord fixture shared across the
// upload-path tests.
func baseImageRecord() api.ImageRecord {
	return api.ImageRecord{
		Id:        testUUID,
		Title:     "a title",
		FileName:  testUUID2 + ".jpg",
		FileType:  "image/jpeg",
		ObjectKey: "uploads/" + testUUID2 + ".jpg",
		Slug:      testUUID2,
		SlugIndex: "slugidx",
	}
}

func TestImagePipeline_GetImageRecord(t *testing.T) {

	tests := []struct {
		name    string
		slug    string
		repo    *mockRepository
		indexer *mockIndexer
		cryptor *mockCryptor
		wantErr bool
	}{
		{
			name:    "invalid slug is rejected before touching the indexer or db",
			slug:    "not-a-uuid",
			repo:    &mockRepository{},
			indexer: &mockIndexer{},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name: "indexer error propagates",
			slug: testUUID,
			indexer: &mockIndexer{
				obtainBlindIndexFn: func(s string) (string, error) { return "", fmt.Errorf("hmac failure") },
			},
			repo:    &mockRepository{},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:    "db lookup error propagates",
			slug:    testUUID,
			indexer: &mockIndexer{},
			repo: &mockRepository{
				findImageFn: func(slugIndex string) (*api.ImageRecord, error) {
					return nil, fmt.Errorf("no image record found")
				},
			},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:    "decrypt error propagates",
			slug:    testUUID,
			indexer: &mockIndexer{},
			repo: &mockRepository{
				findImageFn: func(slugIndex string) (*api.ImageRecord, error) {
					img := baseImageRecord()
					return &img, nil
				},
			},
			cryptor: &mockCryptor{
				decryptImageRecordFn: func(i *api.ImageRecord) error { return fmt.Errorf("bad key") },
			},
			wantErr: true,
		},
		{
			name:    "success",
			slug:    testUUID,
			indexer: &mockIndexer{},
			repo: &mockRepository{
				findImageFn: func(slugIndex string) (*api.ImageRecord, error) {
					img := baseImageRecord()
					return &img, nil
				},
			},
			cryptor: &mockCryptor{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &imagePipeline{db: tt.repo, indexer: tt.indexer, cryptor: tt.cryptor, logger: newDiscardLogger()}

			got, err := p.getImageRecord(tt.slug)
			if (err != nil) != tt.wantErr {
				t.Fatalf("getImageRecord() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got == nil || got.Id != testUUID {
				t.Errorf("getImageRecord() = %+v, want record with Id %s", got, testUUID)
			}
		})
	}
}

func TestImagePipeline_UpdateImageRecord(t *testing.T) {

	tests := []struct {
		name          string
		img           *api.ImageRecord
		cryptor       *mockCryptor
		repo          *mockRepository
		wantErr       bool
		wantDbUpdated bool
	}{
		{
			name:    "nil record is rejected",
			img:     nil,
			cryptor: &mockCryptor{},
			repo:    &mockRepository{},
			wantErr: true,
		},
		{
			name: "encrypt error prevents the db write",
			img:  func() *api.ImageRecord { r := baseImageRecord(); return &r }(),
			cryptor: &mockCryptor{
				encryptImageRecordFn: func(i *api.ImageRecord) error { return fmt.Errorf("encrypt failure") },
			},
			repo:    &mockRepository{},
			wantErr: true,
		},
		{
			name:    "db error propagates",
			img:     func() *api.ImageRecord { r := baseImageRecord(); return &r }(),
			cryptor: &mockCryptor{},
			repo: &mockRepository{
				updateImageFn: func(record api.ImageRecord) error { return fmt.Errorf("connection lost") },
			},
			wantErr:       true,
			wantDbUpdated: true, // the call happens; it's the call's return value that's the error
		},
		{
			name:          "success updates the db",
			img:           func() *api.ImageRecord { r := baseImageRecord(); return &r }(),
			cryptor:       &mockCryptor{},
			repo:          &mockRepository{},
			wantDbUpdated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &imagePipeline{db: tt.repo, cryptor: tt.cryptor, logger: newDiscardLogger()}

			err := p.updateImageRecord(tt.img)
			if (err != nil) != tt.wantErr {
				t.Fatalf("updateImageRecord() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := len(tt.repo.updateImageCalls); (got == 1) != tt.wantDbUpdated {
				t.Errorf("db.UpdateImage call count = %d, wantCalled %v", got, tt.wantDbUpdated)
			}
		})
	}
}

func TestImagePipeline_GetImageAlbums(t *testing.T) {

	tests := []struct {
		name    string
		repo    *mockRepository
		cryptor *mockCryptor
		want    map[string]struct{}
		wantErr bool
	}{
		{
			name: "db error propagates",
			repo: &mockRepository{
				findImageAlbumsFn: func(imageId string) ([]api.AlbumRecord, error) {
					return nil, fmt.Errorf("timeout")
				},
			},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name: "no albums yet returns an empty map",
			repo: &mockRepository{
				findImageAlbumsFn: func(imageId string) ([]api.AlbumRecord, error) { return nil, nil },
			},
			cryptor: &mockCryptor{},
			want:    map[string]struct{}{},
		},
		{
			name: "decrypt error propagates",
			repo: &mockRepository{
				findImageAlbumsFn: func(imageId string) ([]api.AlbumRecord, error) {
					return []api.AlbumRecord{{Id: testUUID, Title: "2024"}}, nil
				},
			},
			cryptor: &mockCryptor{
				decryptAlbumRecordFn: func(a *api.AlbumRecord) error { return fmt.Errorf("bad key") },
			},
			wantErr: true,
		},
		{
			name: "success maps albums by title",
			repo: &mockRepository{
				findImageAlbumsFn: func(imageId string) ([]api.AlbumRecord, error) {
					return []api.AlbumRecord{{Title: "2024"}, {Title: "2023"}}, nil
				},
			},
			cryptor: &mockCryptor{},
			want:    map[string]struct{}{"2024": {}, "2023": {}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &imagePipeline{db: tt.repo, cryptor: tt.cryptor, logger: newDiscardLogger()}

			got, err := p.getImageAlbums(testUUID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("getImageAlbums() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("getImageAlbums() = %v, want %v", got, tt.want)
			}
			for k := range tt.want {
				if _, ok := got[k]; !ok {
					t.Errorf("getImageAlbums() missing key %q", k)
				}
			}
		})
	}
}

func TestImagePipeline_LinkToAlbum(t *testing.T) {

	validImg := &api.ImageRecord{Id: testUUID}

	tests := []struct {
		name           string
		title          string
		img            *api.ImageRecord
		repo           *mockRepository
		cryptor        *mockCryptor
		wantErr        bool
		wantInsertNew  bool
		wantXrefCalled bool
		wantAlbumId    string // only checked when wantXrefCalled
	}{
		{
			name:    "empty title is rejected",
			title:   "",
			img:     validImg,
			repo:    &mockRepository{},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:    "invalid title is rejected",
			title:   "###invalid###",
			img:     validImg,
			repo:    &mockRepository{},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:    "nil image is rejected",
			title:   "2024",
			img:     nil,
			repo:    &mockRepository{},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:    "invalid image id is rejected",
			title:   "2024",
			img:     &api.ImageRecord{Id: "not-a-uuid"},
			repo:    &mockRepository{},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:  "db error listing albums propagates",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, fmt.Errorf("timeout") },
			},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:  "decrypt error while scanning existing albums propagates",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) {
					return []api.AlbumRecord{{Id: testUUID2, Title: "2023"}}, nil
				},
			},
			cryptor: &mockCryptor{
				decryptAlbumRecordFn: func(a *api.AlbumRecord) error { return fmt.Errorf("bad key") },
			},
			wantErr: true,
		},
		{
			name:  "existing album is reused, no new album inserted",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) {
					return []api.AlbumRecord{{Id: "existing-album-id", Title: "2024"}}, nil
				},
			},
			cryptor:        &mockCryptor{},
			wantXrefCalled: true,
			wantAlbumId:    "existing-album-id",
		},
		{
			name:  "xref insert error on existing album propagates",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) {
					return []api.AlbumRecord{{Id: "existing-album-id", Title: "2024"}}, nil
				},
				insertAlbumImageXrefFn: func(xref api.AlbumImageXref) error { return fmt.Errorf("fk violation") },
			},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:  "no matching album: encrypt failure prevents insert",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
			},
			cryptor: &mockCryptor{
				encryptAlbumRecordFn: func(a *api.AlbumRecord) error { return fmt.Errorf("encrypt failure") },
			},
			wantErr: true,
		},
		{
			name:  "no matching album: db insert failure propagates",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
				insertAlbumFn:   func(record api.AlbumRecord) error { return fmt.Errorf("duplicate entry") },
			},
			cryptor: &mockCryptor{},
			wantErr: true,
		},
		{
			name:  "no matching album: creates a new one and links it",
			title: "2024",
			img:   validImg,
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
			},
			cryptor:        &mockCryptor{},
			wantInsertNew:  true,
			wantXrefCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &imagePipeline{db: tt.repo, cryptor: tt.cryptor, indexer: &mockIndexer{}, logger: newDiscardLogger()}

			err := p.linkToAlbum(tt.title, tt.img)
			if (err != nil) != tt.wantErr {
				t.Fatalf("linkToAlbum() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if got := len(tt.repo.insertAlbumCalls); (got == 1) != tt.wantInsertNew {
				t.Errorf("InsertAlbum call count = %d, want new album inserted = %v", got, tt.wantInsertNew)
			}
			if got := len(tt.repo.insertAlbumXrefCalls); (got == 1) != tt.wantXrefCalled {
				t.Errorf("InsertAlbumImageXref call count = %d, want called = %v", got, tt.wantXrefCalled)
			}
			if tt.wantAlbumId != "" && len(tt.repo.insertAlbumXrefCalls) == 1 {
				if got := tt.repo.insertAlbumXrefCalls[0].AlbumId; got != tt.wantAlbumId {
					t.Errorf("xref AlbumId = %q, want %q", got, tt.wantAlbumId)
				}
			}
		})
	}
}

func TestImagePipeline_ResizeAndPut(t *testing.T) {

	src := image.NewRGBA(image.Rect(0, 0, 10, 10))

	tests := []struct {
		name        string
		objStore    *mockObjectStorage
		wantErr     bool
		wantPutCall bool
	}{
		{
			name: "put error propagates",
			objStore: &mockObjectStorage{
				putObjectFn: func(ctx context.Context, key string, data []byte, contentType string) error {
					return fmt.Errorf("storage unavailable")
				},
			},
			wantErr:     true,
			wantPutCall: true,
		},
		{
			name:        "success uploads the resized+encoded image",
			objStore:    &mockObjectStorage{},
			wantPutCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &imagePipeline{objStore: tt.objStore, logger: newDiscardLogger()}

			err := p.resizeAndPut(context.Background(), src, 5, "2024/"+testUUID+"_w5.jpg", "image/jpeg")
			if (err != nil) != tt.wantErr {
				t.Fatalf("resizeAndPut() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := len(tt.objStore.putObjectCalls); (got == 1) != tt.wantPutCall {
				t.Errorf("PutObject call count = %d, want called = %v", got, tt.wantPutCall)
			}
		})
	}
}

// noExifJpeg returns a plain, EXIF-free jpeg of the given size, used to drive
// processImgUpload down its "no date found -> staging" branch, which is
// exercised deterministically below. The complementary "exif date found ->
// year album" branch requires an image with a real, embedded EXIF
// DateTimeOriginal tag; fabricating one is out of scope for this fixture, so
// that branch is instead covered indirectly via the linkToAlbum tests above
// (the function processImgUpload calls once it has computed a year).
func noExifJpeg(t *testing.T, w, h int) []byte {
	t.Helper()
	return encodeJpeg(t, w, h, color.Gray{Y: 128})
}

func TestImagePipeline_ProcessImgUpload(t *testing.T) {

	validKey := "gallery-bucket/uploads/" + testUUID2 + ".jpg"

	tests := []struct {
		name             string
		webhookKey       string
		withObjectFn     func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error
		repo             *mockRepository
		wantUpdateCalled bool
		wantPublished    bool
		wantObjectKey    string
	}{
		{
			name:       "invalid webhook key is rejected before any object storage access",
			webhookKey: "not a valid /// key",
			withObjectFn: func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
				t.Fatal("WithObject should not be called for an unparseable key")
				return nil
			},
			repo: &mockRepository{},
		},
		{
			name:       "object storage error surfaces without a db update",
			webhookKey: validKey,
			withObjectFn: func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
				return fmt.Errorf("object not found")
			},
			repo: &mockRepository{},
		},
		{
			name:       "getImageRecord failure aborts before any writes",
			webhookKey: validKey,
			withObjectFn: func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
				return fn(newFakeReadSeekCloser(noExifJpeg(t, 100, 50)))
			},
			repo: &mockRepository{
				findImageFn: func(slugIndex string) (*api.ImageRecord, error) {
					return nil, fmt.Errorf("no image record found")
				},
			},
		},
		{
			name:       "success with no exif date lands the image in staging, unpublished",
			webhookKey: validKey,
			withObjectFn: func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
				return fn(newFakeReadSeekCloser(noExifJpeg(t, 100, 50)))
			},
			repo: &mockRepository{
				findImageFn: func(slugIndex string) (*api.ImageRecord, error) {
					img := baseImageRecord()
					return &img, nil
				},
			},
			wantUpdateCalled: true,
			wantPublished:    false,
			wantObjectKey:    "staging/" + testUUID2 + ".jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objStore := &mockObjectStorage{withObjectFn: tt.withObjectFn}

			p := &imagePipeline{
				db:       tt.repo,
				indexer:  &mockIndexer{},
				cryptor:  &mockCryptor{},
				objStore: objStore,
				logger:   newDiscardLogger(),
			}

			webhook := storage.WebhookPutObject{MinioKey: tt.webhookKey}
			p.processImgUpload(context.Background(), webhook)

			if got := len(tt.repo.updateImageCalls); (got == 1) != tt.wantUpdateCalled {
				t.Fatalf("UpdateImage call count = %d, want called = %v", got, tt.wantUpdateCalled)
			}
			if !tt.wantUpdateCalled {
				return
			}

			updated := tt.repo.updateImageCalls[0]
			if updated.IsPublished != tt.wantPublished {
				t.Errorf("IsPublished = %v, want %v", updated.IsPublished, tt.wantPublished)
			}
			if updated.ObjectKey != tt.wantObjectKey {
				t.Errorf("ObjectKey = %q, want %q", updated.ObjectKey, tt.wantObjectKey)
			}

			// every configured resolution, tile, and blur derivative plus the
			// original move should have been attempted.
			wantPuts := len(util.ResolutionWidthsImages) + len(util.ResolutionWidthsTiles) + 1
			if got := len(objStore.putObjectCalls); got != wantPuts {
				t.Errorf("PutObject call count = %d, want %d", got, wantPuts)
			}
			if got := len(objStore.moveObjectCalls); got != 1 {
				t.Errorf("MoveObject call count = %d, want 1", got)
			}
		})
	}
}

func TestImagePipeline_UploadQueue(t *testing.T) {

	up := make(chan storage.WebhookPutObject, 1)
	repo := &mockRepository{
		findImageFn: func(slugIndex string) (*api.ImageRecord, error) {
			img := baseImageRecord()
			return &img, nil
		},
	}
	objStore := &mockObjectStorage{
		withObjectFn: func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
			return fn(newFakeReadSeekCloser(noExifJpeg(t, 20, 20)))
		},
	}

	var wg sync.WaitGroup
	p := &imagePipeline{
		uploadQueue: up,
		wg:          &wg,
		db:          repo,
		indexer:     &mockIndexer{},
		cryptor:     &mockCryptor{},
		objStore:    objStore,
		logger:      newDiscardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go p.UploadQueue(ctx)

	up <- storage.WebhookPutObject{MinioKey: "gallery-bucket/uploads/" + testUUID2 + ".jpg"}

	// give the goroutine a moment to drain the item, then shut it down.
	deadline := time.After(2 * time.Second)
	for repo.updateImageCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for UploadQueue to process the webhook")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	wg.Wait() // must return promptly once ctx is cancelled
}
