package pipeline

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tdeslauriers/pixie/pkg/api"
)

var albumColumns = []string{"uuid", "title", "description", "slug", "slug_index", "created_at", "updated_at", "is_archived"}

func albumRow(a api.AlbumRecord) fakeRow {
	return fakeRow{a.Id, a.Title, a.Description, a.Slug, a.SlugIndex, a.CreatedAt.Time, a.UpdatedAt.Time, a.IsArchived}
}

var imageColumns = []string{
	"uuid", "title", "description", "file_name", "file_type", "object_key",
	"slug", "slug_index", "width", "height", "size", "image_date",
	"created_at", "updated_at", "is_archived", "is_published",
}

func imageRow(i api.ImageRecord) fakeRow {
	return fakeRow{
		i.Id, i.Title, i.Description, i.FileName, i.FileType, i.ObjectKey,
		i.Slug, i.SlugIndex, int64(i.Width), int64(i.Height), i.Size, i.ImageDate,
		i.CreatedAt.Time, i.UpdatedAt.Time, i.IsArchived, i.IsPublished,
	}
}

func sampleAlbum() api.AlbumRecord {
	now := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	return api.AlbumRecord{
		Id:          "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Title:       "2024",
		Description: "Auto-generated album for year 2024",
		Slug:        "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		SlugIndex:   "deadbeef",
		CreatedAt:   dataCustomTime(now),
		UpdatedAt:   dataCustomTime(now),
		IsArchived:  false,
	}
}

func sampleImage() api.ImageRecord {
	now := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	return api.ImageRecord{
		Id:          "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Title:       "encrypted-title",
		Description: "encrypted-description",
		FileName:    "dddddddd-dddd-dddd-dddd-dddddddddddd.jpg",
		FileType:    "image/jpeg",
		ObjectKey:   "2024/dddddddd-dddd-dddd-dddd-dddddddddddd.jpg",
		Slug:        "dddddddd-dddd-dddd-dddd-dddddddddddd",
		SlugIndex:   "slugidx",
		Width:       1920,
		Height:      1080,
		Size:        123456,
		ImageDate:   "2024-03-01T12:00:00Z",
		CreatedAt:   dataCustomTime(now),
		UpdatedAt:   dataCustomTime(now),
		IsArchived:  false,
		IsPublished: true,
	}
}

func TestRepository_FindAllAlbums(t *testing.T) {

	tests := []struct {
		name    string
		queryFn func(query string, args []driver.Value) ([]string, []fakeRow, error)
		want    []api.AlbumRecord
		wantErr bool
	}{
		{
			name: "success multiple rows",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				a1, a2 := sampleAlbum(), sampleAlbum()
				a2.Id, a2.Title = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", "2023"
				return albumColumns, []fakeRow{albumRow(a1), albumRow(a2)}, nil
			},
			want: func() []api.AlbumRecord {
				a1, a2 := sampleAlbum(), sampleAlbum()
				a2.Id, a2.Title = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", "2023"
				return []api.AlbumRecord{a1, a2}
			}(),
		},
		{
			name: "no rows returns empty slice, no error",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				return albumColumns, nil, nil
			},
			want: nil,
		},
		{
			name: "query error propagates",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				return nil, nil, fmt.Errorf("connection reset")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newFakeDB(t, &fakeConn{queryFn: tt.queryFn})
			repo := NewRepository(db)

			got, err := repo.FindAllAlbums()
			if (err != nil) != tt.wantErr {
				t.Fatalf("FindAllAlbums() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindAllAlbums() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRepository_FindImage(t *testing.T) {

	tests := []struct {
		name      string
		slugIndex string
		queryFn   func(query string, args []driver.Value) ([]string, []fakeRow, error)
		want      *api.ImageRecord
		wantErr   string // substring expected in error, empty means no error
	}{
		{
			name:      "found",
			slugIndex: "slugidx",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				if len(args) != 1 || args[0] != "slugidx" {
					t.Fatalf("expected query arg [slugidx], got %v", args)
				}
				img := sampleImage()
				return imageColumns, []fakeRow{imageRow(img)}, nil
			},
			want: func() *api.ImageRecord { i := sampleImage(); return &i }(),
		},
		{
			name:      "not found maps to friendly error",
			slugIndex: "missing",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				return imageColumns, nil, nil
			},
			wantErr: "no image record found",
		},
		{
			name:      "underlying db error propagates",
			slugIndex: "slugidx",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				return nil, nil, fmt.Errorf("driver: bad connection")
			},
			wantErr: "bad connection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newFakeDB(t, &fakeConn{queryFn: tt.queryFn})
			repo := NewRepository(db)

			got, err := repo.FindImage(tt.slugIndex)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("FindImage() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindImage() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindImage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRepository_FindImageAlbums(t *testing.T) {

	tests := []struct {
		name    string
		imageId string
		queryFn func(query string, args []driver.Value) ([]string, []fakeRow, error)
		want    []api.AlbumRecord
		wantErr bool
	}{
		{
			name:    "success",
			imageId: "image-id",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				if len(args) != 1 || args[0] != "image-id" {
					t.Fatalf("expected query arg [image-id], got %v", args)
				}
				return albumColumns, []fakeRow{albumRow(sampleAlbum())}, nil
			},
			want: []api.AlbumRecord{sampleAlbum()},
		},
		{
			name:    "no linked albums yet",
			imageId: "image-id",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				return albumColumns, nil, nil
			},
			want: nil,
		},
		{
			name:    "query error propagates",
			imageId: "image-id",
			queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
				return nil, nil, fmt.Errorf("timeout")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newFakeDB(t, &fakeConn{queryFn: tt.queryFn})
			repo := NewRepository(db)

			got, err := repo.FindImageAlbums(tt.imageId)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FindImageAlbums() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindImageAlbums() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRepository_InsertAlbum(t *testing.T) {

	tests := []struct {
		name    string
		record  api.AlbumRecord
		execFn  func(query string, args []driver.Value) (int64, int64, error)
		wantErr bool
	}{
		{
			name:   "success",
			record: sampleAlbum(),
			execFn: func(query string, args []driver.Value) (int64, int64, error) {
				if len(args) != 8 {
					t.Fatalf("expected 8 args for album insert, got %d: %v", len(args), args)
				}
				if args[0] != sampleAlbum().Id || args[1] != sampleAlbum().Title {
					t.Fatalf("unexpected args order: %v", args)
				}
				return 0, 1, nil
			},
		},
		{
			name:   "db error propagates",
			record: sampleAlbum(),
			execFn: func(query string, args []driver.Value) (int64, int64, error) {
				return 0, 0, fmt.Errorf("duplicate entry")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newFakeDB(t, &fakeConn{execFn: tt.execFn})
			repo := NewRepository(db)

			err := repo.InsertAlbum(tt.record)
			if (err != nil) != tt.wantErr {
				t.Fatalf("InsertAlbum() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRepository_InsertAlbumImageXref(t *testing.T) {

	xref := api.AlbumImageXref{
		Id:        0,
		AlbumId:   "album-id",
		ImageId:   "image-id",
		CreatedAt: dataCustomTime(time.Now().UTC()),
	}

	tests := []struct {
		name    string
		execFn  func(query string, args []driver.Value) (int64, int64, error)
		wantErr bool
	}{
		{
			name: "success (including duplicate-key no-op)",
			execFn: func(query string, args []driver.Value) (int64, int64, error) {
				if len(args) != 4 {
					t.Fatalf("expected 4 args for xref insert, got %d: %v", len(args), args)
				}
				if args[1] != "album-id" || args[2] != "image-id" {
					t.Fatalf("unexpected args: %v", args)
				}
				return 0, 0, nil // ON DUPLICATE KEY no-op affects 0 rows
			},
		},
		{
			name: "db error propagates",
			execFn: func(query string, args []driver.Value) (int64, int64, error) {
				return 0, 0, fmt.Errorf("fk constraint violation")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newFakeDB(t, &fakeConn{execFn: tt.execFn})
			repo := NewRepository(db)

			err := repo.InsertAlbumImageXref(xref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("InsertAlbumImageXref() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRepository_UpdateImage(t *testing.T) {

	img := sampleImage()

	tests := []struct {
		name    string
		execFn  func(query string, args []driver.Value) (int64, int64, error)
		wantErr bool
	}{
		{
			name: "success, args in expected order with id last",
			execFn: func(query string, args []driver.Value) (int64, int64, error) {
				// data.CustomTime implements driver.Valuer, which database/sql
				// invokes automatically before handing the arg to the driver,
				// so the value that reaches Exec is the formatted string, not
				// a time.Time.
				want := []driver.Value{
					img.ObjectKey, int64(img.Width), int64(img.Height), img.ImageDate,
					img.UpdatedAt.Time.UTC().Format("2006-01-02 15:04:05"), img.IsPublished, img.Id,
				}
				if len(args) != len(want) {
					t.Fatalf("expected %d args, got %d: %v", len(want), len(args), args)
				}
				for i := range want {
					if args[i] != want[i] {
						t.Fatalf("arg[%d] = %v, want %v", i, args[i], want[i])
					}
				}
				return 0, 1, nil
			},
		},
		{
			name: "db error propagates",
			execFn: func(query string, args []driver.Value) (int64, int64, error) {
				return 0, 0, fmt.Errorf("connection lost")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newFakeDB(t, &fakeConn{execFn: tt.execFn})
			repo := NewRepository(db)

			err := repo.UpdateImage(img)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UpdateImage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestRepository_FindImage_ErrNoRowsMapping specifically pins down that
// sql.ErrNoRows (not just "any error") is what triggers the friendly
// "no image record found" message, since that's the exact check in the
// implementation (`if err == sql.ErrNoRows`).
func TestRepository_FindImage_ErrNoRowsMapping(t *testing.T) {
	db := newFakeDB(t, &fakeConn{
		queryFn: func(query string, args []driver.Value) ([]string, []fakeRow, error) {
			return imageColumns, nil, nil // Next() returns io.EOF -> sql.ErrNoRows via QueryRow.Scan
		},
	})
	repo := NewRepository(db)

	_, err := repo.FindImage("nope")
	if err == nil {
		t.Fatal("expected error for missing image record, got nil")
	}
	if got, want := err.Error(), "no image record found"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected the raw sql.ErrNoRows to be wrapped into a friendly message, not passed through as-is")
	}
}
