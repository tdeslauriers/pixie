package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

func TestReprocessBackoff(t *testing.T) {

	tests := []struct {
		name        string
		attempt     int
		wantMin     time.Duration
		wantMaxExcl time.Duration // result must be strictly less than this
	}{
		{"attempt 0 is treated as attempt 1", 0, ReprocessBaseBackoff / 2, ReprocessBaseBackoff},
		{"attempt 1", 1, ReprocessBaseBackoff / 2, ReprocessBaseBackoff},
		{"attempt 2 doubles the base", 2, ReprocessBaseBackoff, 2 * ReprocessBaseBackoff},
		{"attempt 3 doubles again", 3, 2 * ReprocessBaseBackoff, 4 * ReprocessBaseBackoff},
		{"large attempt is capped at the max", 30, ReprocessMaxBackoff / 2, ReprocessMaxBackoff},
		{"negative attempt is treated as attempt 1", -5, ReprocessBaseBackoff / 2, ReprocessBaseBackoff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// jitter makes this non-deterministic, so run several times and
			// check every draw lands in the expected [min, max) half-open window.
			for i := 0; i < 25; i++ {
				got := reprocessBackoff(tt.attempt)
				if got < tt.wantMin || got >= tt.wantMaxExcl {
					t.Fatalf("reprocessBackoff(%d) = %v, want in [%v, %v)", tt.attempt, got, tt.wantMin, tt.wantMaxExcl)
				}
			}
		})
	}
}

func TestRequeueReprocess_MaxRetriesDropsImmediately(t *testing.T) {

	reprocessQueue := make(chan ReprocessCmd, 1)
	var wg sync.WaitGroup
	p := &imagePipeline{reprocessQueue: reprocessQueue, wg: &wg, logger: newDiscardLogger()}

	p.requeueReprocess(context.Background(), newDiscardLogger(), ReprocessCmd{RetryCount: MaxReprocessRetries - 1})

	select {
	case <-reprocessQueue:
		t.Error("expected no requeue send once retries are exhausted")
	default:
	}
}

func TestRequeueReprocess_ContextCancelledSkipsBackoff(t *testing.T) {

	reprocessQueue := make(chan ReprocessCmd, 1)
	var wg sync.WaitGroup
	p := &imagePipeline{reprocessQueue: reprocessQueue, wg: &wg, logger: newDiscardLogger()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the backoff timer even starts

	p.requeueReprocess(ctx, newDiscardLogger(), ReprocessCmd{RetryCount: 0})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("requeueReprocess's goroutine did not exit promptly when ctx was already cancelled")
	}

	select {
	case <-reprocessQueue:
		t.Error("expected no requeue send once ctx was cancelled")
	default:
	}
}

func TestRequeueReprocess_SchedulesRetryAfterBackoff(t *testing.T) {

	reprocessQueue := make(chan ReprocessCmd, 1)
	var wg sync.WaitGroup
	p := &imagePipeline{reprocessQueue: reprocessQueue, wg: &wg, logger: newDiscardLogger()}

	p.requeueReprocess(context.Background(), newDiscardLogger(), ReprocessCmd{RetryCount: 0, Slug: testUUID})

	select {
	case cmd := <-reprocessQueue:
		if cmd.RetryCount != 1 {
			t.Errorf("requeued RetryCount = %d, want 1", cmd.RetryCount)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for the backed-off retry to be requeued")
	}

	wg.Wait()
}

// baseReprocessCmd is a valid ReprocessCmd fixture: a "staging" image being
// (re)moved into its computed year directory, "2024".
func baseReprocessCmd() ReprocessCmd {
	return ReprocessCmd{
		Id:            testUUID,
		FileName:      testUUID2 + ".jpg",
		FileType:      "image/jpeg",
		Slug:          testUUID2,
		CurrentObjKey: "staging/" + testUUID2 + ".jpg",
		UpdatedObjKey: "2024/" + testUUID2 + ".jpg",
		MoveRequired:  true,
	}
}

func errNotExist(key string) error {
	return fmt.Errorf("object %s does not exist in object storage", key)
}

func TestImagePipeline_ProcessReprocessCmd(t *testing.T) {

	alwaysSucceedMove := func(ctx context.Context, src, dst string) error { return nil }

	tests := []struct {
		name     string
		cmd      ReprocessCmd
		repo     *mockRepository
		objStore *mockObjectStorage

		wantNoMoveCalls  bool
		wantAlbumCreated bool
	}{
		{
			name:            "already at max retries is a no-op",
			cmd:             func() ReprocessCmd { c := baseReprocessCmd(); c.RetryCount = MaxReprocessRetries; return c }(),
			repo:            &mockRepository{},
			objStore:        &mockObjectStorage{},
			wantNoMoveCalls: true,
		},
		{
			name:            "MoveRequired false is a no-op",
			cmd:             func() ReprocessCmd { c := baseReprocessCmd(); c.MoveRequired = false; return c }(),
			repo:            &mockRepository{},
			objStore:        &mockObjectStorage{},
			wantNoMoveCalls: true,
		},
		{
			name:            "unparseable current object key is dropped, not requeued",
			cmd:             func() ReprocessCmd { c := baseReprocessCmd(); c.CurrentObjKey = "bad-key-no-ext"; return c }(),
			repo:            &mockRepository{},
			objStore:        &mockObjectStorage{},
			wantNoMoveCalls: true,
		},
		{
			name: "full success: everything already existed and moved cleanly, new year album created",
			cmd:  baseReprocessCmd(),
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
			},
			objStore:         &mockObjectStorage{moveObjectFn: alwaysSucceedMove},
			wantAlbumCreated: true,
		},
		{
			name: "original move fails but destination already has the file: treated as success",
			cmd:  baseReprocessCmd(),
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
			},
			objStore: &mockObjectStorage{
				moveObjectFn: func(ctx context.Context, src, dst string) error {
					if src == baseReprocessCmd().CurrentObjKey {
						return errNotExist(src)
					}
					return nil
				},
				listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) {
					return []string{prefix}, nil // found at the updated location
				},
			},
			wantAlbumCreated: true,
		},
		{
			name: "original not found anywhere requeues (verified by immediate drop at max-1)",
			cmd:  func() ReprocessCmd { c := baseReprocessCmd(); c.RetryCount = MaxReprocessRetries - 1; return c }(),
			repo: &mockRepository{},
			objStore: &mockObjectStorage{
				moveObjectFn: func(ctx context.Context, src, dst string) error { return errNotExist(src) },
				listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) {
					return nil, nil // not found at the destination either
				},
			},
		},
		{
			name: "a non-'does not exist' move failure also requeues",
			cmd:  func() ReprocessCmd { c := baseReprocessCmd(); c.RetryCount = MaxReprocessRetries - 1; return c }(),
			repo: &mockRepository{},
			objStore: &mockObjectStorage{
				moveObjectFn: func(ctx context.Context, src, dst string) error { return fmt.Errorf("permission denied") },
			},
		},
		{
			name: "unparseable updated object key drops after a clean move, no album work",
			cmd:  func() ReprocessCmd { c := baseReprocessCmd(); c.UpdatedObjKey = "no-extension-key"; return c }(),
			repo: &mockRepository{},
			objStore: &mockObjectStorage{
				moveObjectFn: func(ctx context.Context, src, dst string) error { return nil },
			},
		},
		{
			name: "non-numeric year directory drops after a clean move, no album work",
			cmd: func() ReprocessCmd {
				c := baseReprocessCmd()
				c.UpdatedObjKey = "not-a-year/" + testUUID2 + ".jpg"
				return c
			}(),
			repo: &mockRepository{},
			objStore: &mockObjectStorage{
				moveObjectFn: func(ctx context.Context, src, dst string) error { return nil },
			},
		},
		{
			name: "album linking failure requeues (verified by immediate drop at max-1)",
			cmd:  func() ReprocessCmd { c := baseReprocessCmd(); c.RetryCount = MaxReprocessRetries - 1; return c }(),
			repo: &mockRepository{
				findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, fmt.Errorf("db unavailable") },
			},
			objStore: &mockObjectStorage{moveObjectFn: alwaysSucceedMove},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wg sync.WaitGroup
			p := &imagePipeline{
				reprocessQueue: make(chan ReprocessCmd, 1),
				wg:             &wg,
				db:             tt.repo,
				indexer:        &mockIndexer{},
				cryptor:        &mockCryptor{},
				objStore:       tt.objStore,
				logger:         newDiscardLogger(),
			}

			// processReprocessCmd's own requeue calls are exercised here with
			// RetryCount already at MaxReprocessRetries-1, so requeueReprocess's
			// max-retry check fires synchronously with no real backoff wait.
			// Dedicated tests above (TestRequeueReprocess_*) cover the actual
			// backoff/timer/cancellation behavior in isolation. The timeout
			// guard below turns a wrong assumption there into a clear failure
			// instead of a multi-second hang.
			done := make(chan struct{})
			go func() {
				p.processReprocessCmd(context.Background(), tt.cmd)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				t.Fatal("processReprocessCmd did not return promptly")
			}

			if tt.wantNoMoveCalls && len(tt.objStore.moveObjectCalls) != 0 {
				t.Errorf("MoveObject call count = %d, want 0", len(tt.objStore.moveObjectCalls))
			}

			if got := len(tt.repo.insertAlbumCalls); (got == 1) != tt.wantAlbumCreated {
				t.Errorf("InsertAlbum call count = %d, want new album created = %v", got, tt.wantAlbumCreated)
			}
			if tt.wantAlbumCreated {
				if got := len(tt.repo.insertAlbumXrefCalls); got != 1 {
					t.Errorf("InsertAlbumImageXref call count = %d, want 1", got)
				} else if got := tt.repo.insertAlbumXrefCalls[0].ImageId; got != tt.cmd.Id {
					t.Errorf("xref ImageId = %q, want %q", got, tt.cmd.Id)
				}
			}
		})
	}
}

func TestImagePipeline_ProcessReprocessCmd_RebuildsMissingDerivative(t *testing.T) {

	cmd := baseReprocessCmd()
	missingKey := fmt.Sprintf("staging/%s_w%d.jpg", testUUID2, util.ResolutionWidthsImages[0])
	rebuiltKey := fmt.Sprintf("2024/%s_w%d.jpg", testUUID2, util.ResolutionWidthsImages[0])

	repo := &mockRepository{
		findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
	}
	objStore := &mockObjectStorage{
		// every move succeeds except the one resolution derivative that's
		// "missing", which forces the rebuild-from-original path.
		moveObjectFn: func(ctx context.Context, src, dst string) error {
			if src == missingKey {
				return errNotExist(src)
			}
			return nil
		},
		withObjectFn: func(ctx context.Context, key string, fn func(r storage.ReadSeekCloser) error) error {
			if key != cmd.UpdatedObjKey {
				t.Fatalf("WithObject called with unexpected key %q, want the already-moved original %q", key, cmd.UpdatedObjKey)
			}
			return fn(newFakeReadSeekCloser(noExifJpeg(t, 800, 600)))
		},
	}

	var wg sync.WaitGroup
	p := &imagePipeline{
		reprocessQueue: make(chan ReprocessCmd, 1),
		wg:             &wg,
		db:             repo,
		indexer:        &mockIndexer{},
		cryptor:        &mockCryptor{},
		objStore:       objStore,
		logger:         newDiscardLogger(),
	}

	done := make(chan struct{})
	go func() {
		p.processReprocessCmd(context.Background(), cmd)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("processReprocessCmd did not return promptly")
	}

	found := false
	for _, k := range objStore.putObjectCalls {
		if k == rebuiltKey {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PutObject to be called for rebuilt key %q, calls were %v", rebuiltKey, objStore.putObjectCalls)
	}
}

func TestImagePipeline_ReprocessQueue(t *testing.T) {

	re := make(chan ReprocessCmd, 1)
	repo := &mockRepository{
		findAllAlbumsFn: func() ([]api.AlbumRecord, error) { return nil, nil },
	}
	objStore := &mockObjectStorage{
		moveObjectFn: func(ctx context.Context, src, dst string) error { return nil },
	}

	var wg sync.WaitGroup
	p := &imagePipeline{
		reprocessQueue: re,
		wg:             &wg,
		db:             repo,
		indexer:        &mockIndexer{},
		cryptor:        &mockCryptor{},
		objStore:       objStore,
		logger:         newDiscardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go p.ReprocessQueue(ctx)

	re <- baseReprocessCmd()

	deadline := time.After(2 * time.Second)
	for repo.insertAlbumCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for ReprocessQueue to process the command")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	wg.Wait()
}
