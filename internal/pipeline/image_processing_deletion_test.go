package pipeline

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestBuildDeletionPrefixes(t *testing.T) {

	tests := []struct {
		name    string
		cmd     DeletionCmd
		want    []string // compared as a set (order is not guaranteed: dirs is a map)
		wantErr bool
	}{
		{
			name:    "no slug and no object key is an error",
			cmd:     DeletionCmd{},
			wantErr: true,
		},
		{
			name: "slug only sweeps the default upload/staging directories",
			cmd:  DeletionCmd{Slug: testUUID},
			want: []string{"uploads/" + testUUID, "staging/" + testUUID},
		},
		{
			name: "parseable object key adds its own directory to the sweep",
			cmd:  DeletionCmd{ObjectKey: "gallery-bucket/2024/" + testUUID + ".jpg"},
			want: []string{"2024/" + testUUID, "uploads/" + testUUID, "staging/" + testUUID},
		},
		{
			name: "object key directory that duplicates a sweep dir is deduped",
			cmd:  DeletionCmd{ObjectKey: "gallery-bucket/uploads/" + testUUID + ".jpg"},
			want: []string{"uploads/" + testUUID, "staging/" + testUUID},
		},
		{
			name: "unparseable object key is not fatal as long as a slug is available",
			cmd:  DeletionCmd{Slug: testUUID, ObjectKey: "###not-a-valid-key###"},
			want: []string{"uploads/" + testUUID, "staging/" + testUUID},
		},
		{
			name: "slug is derived from the object key when cmd.Slug is empty",
			cmd:  DeletionCmd{ObjectKey: "gallery-bucket/2023/" + testUUID2 + ".png"},
			want: []string{"2023/" + testUUID2, "uploads/" + testUUID2, "staging/" + testUUID2},
		},
		{
			name: "explicit slug wins over one derived from the object key",
			cmd:  DeletionCmd{Slug: testUUID, ObjectKey: "gallery-bucket/2023/" + testUUID2 + ".png"},
			want: []string{"2023/" + testUUID, "uploads/" + testUUID, "staging/" + testUUID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDeletionPrefixes(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildDeletionPrefixes() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)

			if len(got) != len(want) {
				t.Fatalf("buildDeletionPrefixes() = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("buildDeletionPrefixes() = %v, want %v", got, want)
					break
				}
			}
		})
	}
}

func TestImagePipeline_ProcessDeletionCmd(t *testing.T) {

	tests := []struct {
		name            string
		cmd             DeletionCmd
		objStore        *mockObjectStorage
		wantNoListCalls bool
		wantDeleteCall  bool
		wantDeleteKeys  []string
	}{
		{
			name:            "unresolvable slug/object key aborts before touching object storage",
			cmd:             DeletionCmd{},
			objStore:        &mockObjectStorage{},
			wantNoListCalls: true,
		},
		{
			name: "a listing error aborts the sweep without deleting anything",
			cmd:  DeletionCmd{Slug: testUUID},
			objStore: &mockObjectStorage{
				listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) {
					return nil, fmt.Errorf("minio unreachable")
				},
			},
			wantDeleteCall: false,
		},
		{
			name: "no matching objects is not an error and skips the delete call",
			cmd:  DeletionCmd{Slug: testUUID},
			objStore: &mockObjectStorage{
				listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) { return nil, nil },
			},
			wantDeleteCall: false,
		},
		{
			name: "matching objects across prefixes are aggregated into one bulk delete",
			cmd:  DeletionCmd{Slug: testUUID},
			objStore: &mockObjectStorage{
				listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) {
					return []string{prefix + ".jpg", prefix + "_blur.jpg"}, nil
				},
			},
			wantDeleteCall: true,
			wantDeleteKeys: []string{
				"uploads/" + testUUID + ".jpg", "uploads/" + testUUID + "_blur.jpg",
				"staging/" + testUUID + ".jpg", "staging/" + testUUID + "_blur.jpg",
			},
		},
		{
			name: "a delete error is logged, not panicked",
			cmd:  DeletionCmd{Slug: testUUID},
			objStore: &mockObjectStorage{
				listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) {
					return []string{prefix + ".jpg"}, nil
				},
				deleteObjectsFn: func(ctx context.Context, keys []string) error {
					return fmt.Errorf("bulk delete failed")
				},
			},
			wantDeleteCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &imagePipeline{objStore: tt.objStore, logger: newDiscardLogger()}

			p.processDeletionCmd(context.Background(), tt.cmd)

			if tt.wantNoListCalls && len(tt.objStore.listObjectsCalls) != 0 {
				t.Errorf("ListObjects call count = %d, want 0", len(tt.objStore.listObjectsCalls))
			}

			gotDeleteCalled := len(tt.objStore.deleteObjectsCall) == 1
			if gotDeleteCalled != tt.wantDeleteCall {
				t.Fatalf("DeleteObjects called = %v, want %v", gotDeleteCalled, tt.wantDeleteCall)
			}
			if !tt.wantDeleteCall || tt.wantDeleteKeys == nil {
				return
			}

			got := append([]string(nil), tt.objStore.deleteObjectsCall[0]...)
			sort.Strings(got)
			want := append([]string(nil), tt.wantDeleteKeys...)
			sort.Strings(want)

			if len(got) != len(want) {
				t.Fatalf("deleted keys = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("deleted keys = %v, want %v", got, want)
					break
				}
			}
		})
	}
}

func TestImagePipeline_DeletionQueue(t *testing.T) {

	de := make(chan DeletionCmd, 1)
	objStore := &mockObjectStorage{
		listObjectsFn: func(ctx context.Context, prefix string) ([]string, error) {
			return []string{prefix + ".jpg"}, nil
		},
	}

	var wg sync.WaitGroup
	p := &imagePipeline{
		deletionQueue: de,
		wg:            &wg,
		objStore:      objStore,
		logger:        newDiscardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go p.DeletionQueue(ctx)

	de <- DeletionCmd{Slug: testUUID}

	deadline := time.After(2 * time.Second)
	for objStore.deleteObjectsCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for DeletionQueue to process the command")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	wg.Wait()
}
