package picture

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/crypt"
)

// AlbumImageService is an interface that defines the methods for album_image xref management.
// This service is responsible for creating, updating, and deleting album-image xrefs.
type AlbumImageService interface {

	// GetImageAlbums retrieves the albums associated with an image.
	// Returns a map of album slugs to AlbumRecord and a slice of AlbumRecord, ir
	// an error if any.
	GetImageAlbums(imageId string) (map[string]db.AlbumRecord, []db.AlbumRecord, error)

	// InsertImagePermissionXref inserts a new image_permission xref record into the database.
	InsertImagePermissionXref(imageId, permissionId string) error
}

// NewAlbumImageService creates a new AlbumImageService instace and returns a pointer to the concrete implementation.
func NewAlbumImageService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) AlbumImageService {
	return &albumImageService{
		sql:     sql,
		indexer: i,
		cryptor: crypt.NewCryptor(c),

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumImageService)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ AlbumImageService = (*albumImageService)(nil)

// albumImageService is the concrete implementation of the AlbumImageService interface.
// It manages the xref between albums and images.
// This service is responsible for creating, updating, and deleting album-image xrefs.
type albumImageService struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor crypt.Cryptor

	logger *slog.Logger
}

// GetImageAlbums retrieves the albums associated with an image.
// Returns a map of album slugs to AlbumRecord and a slice of AlbumRecord, ir
// an error if any.
func (s *albumImageService) GetImageAlbums(imageId string) (map[string]db.AlbumRecord, []db.AlbumRecord, error) {

	// validate the image id
	if !validate.IsValidUuid(imageId) {
		return nil, nil, fmt.Errorf("image Id must be a valid UUID")
	}

	// build the query to get the albums associated with the image
	qry := `
		SELECT 
			a.uuid,
			a.title,
			a.description,
			a.slug,
			a.slug_index,
			a.created_at,
			a.updated_at,
			a.is_archived
		FROM album a
			LEFT OUTER JOIN album_image ai ON a.uuid = ai.album_uuid
		WHERE ai.image_uuid = ?`
	var albums []db.AlbumRecord
	if err := s.sql.SelectRecords(qry, &albums, imageId); err != nil {
		s.logger.Error(fmt.Sprintf("failed to retrieve albums for image '%s': %v", imageId, err))
		return nil, nil, fmt.Errorf("failed to retrieve albums for image '%s': %v", imageId, err)
	}

	if len(albums) == 0 {
		noneMsg := fmt.Sprintf("no albums found for image '%s'", imageId)
		s.logger.Info(noneMsg)
		return nil, nil, fmt.Errorf("no albums found for image '%s'", imageId)
	}

	s.logger.Info(fmt.Sprintf("retrieved %d albums for image '%s'", len(albums), imageId))

	// decrypt the album records
	var (
		wg    sync.WaitGroup
		errCh = make(chan error, len(albums))
	)

	albumsMap := make(map[string]db.AlbumRecord, len(albums))
	for i := range albums {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.cryptor.DecryptAlbumRecord(&albums[i]); err != nil {
				errCh <- fmt.Errorf("failed to decrypt album record: %v", err)
				return
			}
			albumsMap[albums[i].Slug] = albums[i]
		}(i)
	}

	wg.Wait()
	close(errCh)

	// check for errors during decryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, nil, fmt.Errorf("failed to decrypt album records: %v", errors.Join(errs...))
		}
	}

	// return the decrypted albums
	s.logger.Info(fmt.Sprintf("successfully decrypted %d albums for image '%s'", len(albums), imageId))

	return albumsMap, albums, nil
}

// InsertImagePermissionXref is the concrete implentation of then interface method which
// inserts a new image_permission xref record into the database.
func (s *albumImageService) InsertImagePermissionXref(imageId, permissionId string) error {

	// validate the image id
	if !validate.IsValidUuid(imageId) {
		return fmt.Errorf("image Id must be a valid UUID")
	}

	// validate the permission id
	if !validate.IsValidUuid(permissionId) {
		return fmt.Errorf("permission Id must be a valid UUID")
	}

	// build the xref record to insert
	xref := db.ImagePermissionXref{
		Id:           0, // auto-incremented by the database
		ImageId:      imageId,
		PermissionId: permissionId,
		CreatedAt:    data.CustomTime{Time: time.Now().UTC()},
	}
	qry := `
		INSERT INTO image_permission (
			id, 
			image_uuid, 
			permission_uuid, 
			created_at
		) VALUES (?, ?, ?, ?)`
	if err := s.sql.InsertRecord(qry, xref); err != nil {
		return fmt.Errorf("failed to insert image_permission xref record: %v", err)
	}

	s.logger.Info(fmt.Sprintf("inserted image_permission xref record for image '%s' and permission '%s'", imageId, permissionId))
	return nil
}
