package picture

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect"
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

	// UpdateAlbumImages updates the albums associated with an image.
	// It adds new associations and removes old ones.
	// Returns an error if any operation fails.
	UpdateAlbumImages(ctx context.Context, imageId string, albumSlugs []string) error
}

// NewAlbumImageService creates a new AlbumImageService instace and returns a pointer to the concrete implementation.
func NewAlbumImageService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) AlbumImageService {
	return &albumImageService{
		sql:     sql,
		indexer: i,
		cryptor: crypt.NewCryptor(c),

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumImageService)),
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
		return nil, nil, fmt.Errorf("failed to retrieve albums for image '%s': %v", imageId, err)
	}

	if len(albums) == 0 {
		return nil, nil, fmt.Errorf("no albums found for image '%s'", imageId)
	}

	// decrypt the album records
	var (
		wg    sync.WaitGroup
		errCh = make(chan error, len(albums))

		albumsMap = make(map[string]db.AlbumRecord, len(albums))
		mu        sync.Mutex
	)

	for i := range albums {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.cryptor.DecryptAlbumRecord(&albums[i]); err != nil {
				errCh <- fmt.Errorf("failed to decrypt album record: %v", err)
				return
			}
			mu.Lock()
			albumsMap[albums[i].Slug] = albums[i]
			mu.Unlock()
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

// UpdateAlbumImages updates the albums associated with an image.
// It adds new associations and removes old ones.
// Returns an error if any operation fails.
func (s *albumImageService) UpdateAlbumImages(ctx context.Context, imageId string, albumSlugs []string) error {

	// create function scoped logger
	// add telemetry fields from context if exists
	log := s.logger
	if tel, ok := connect.GetTelemetryFromContext(ctx); ok && tel != nil {
		log = log.With(tel.TelemetryFields()...)
	} else {
		log.Warn("no telemetry found in context for UpdateAlbumImages")
	}

	// validate the image id
	if !validate.IsValidUuid(imageId) {
		return fmt.Errorf("image Id must be a valid UUID")
	}

	// if no album slugs provided, nothing to do
	if len(albumSlugs) == 0 {
		s.logger.Warn(fmt.Sprintf("no album slugs provided for image '%s', nothing to update", imageId))
		return nil
	}

	// validate each album slug
	for _, slug := range albumSlugs {
		if !validate.IsValidUuid(slug) {
			return fmt.Errorf("album slug '%s' must be a valid UUID", slug)
		}
	}

	// get all albums to validate the cmd album slugs exist
	qry := `SELECT uuid, title, description, slug, slug_index, created_at, updated_at, is_archived FROM album`
	var allAlbums []db.AlbumRecord
	if err := s.sql.SelectRecords(qry, &allAlbums); err != nil {
		return fmt.Errorf("failed to retrieve all albums for validation: %v", err)
	}

	// decrypt and build a map of all albums
	var (
		wg    sync.WaitGroup
		errCh = make(chan error, len(allAlbums))
	)
	allAlbumsMap := make(map[string]db.AlbumRecord, len(allAlbums))
	mu := &sync.Mutex{}

	for i := range allAlbums {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.cryptor.DecryptAlbumRecord(&allAlbums[i]); err != nil {
				errCh <- fmt.Errorf("failed to decrypt album record: %v", err)
				return
			}
			mu.Lock()
			allAlbumsMap[allAlbums[i].Slug] = allAlbums[i]
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	close(errCh)

	// log errors if any
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to decrypt some album records: %v", errors.Join(errs...))
		}
	}

	// build a map of the new album slugs for easy lookup
	newAlbumsMap := make(map[string]db.AlbumRecord, len(albumSlugs))

	// check that each provided album slug exists and if so add to the newAlbumsMap
	for _, slug := range albumSlugs {
		if album, exists := allAlbumsMap[slug]; !exists {
			return fmt.Errorf("album with slug '%s' does not exist", slug)
		} else {
			newAlbumsMap[slug] = album
		}
	}

	// get the current albums associated with the image
	currentAlbumsMap, _, err := s.GetImageAlbums(imageId)
	if err != nil {
		if strings.Contains(err.Error(), "no albums found for image") {
			log.Warn(fmt.Sprintf("image '%s' currently has no albums associated", imageId))
		} else {
			return fmt.Errorf("failed to get current albums for image '%s': %v", imageId, err)
		}
	}

	// determine which albums need to be added and which need to be removed
	var (
		toAdd    []db.AlbumRecord
		toRemove []db.AlbumRecord
	)

	for slug, album := range newAlbumsMap {
		if _, exists := currentAlbumsMap[slug]; !exists {
			toAdd = append(toAdd, album)
		}
	}

	for slug, album := range currentAlbumsMap {
		if _, exists := newAlbumsMap[slug]; !exists {
			toRemove = append(toRemove, album)
		}
	}

	if len(toAdd) > 0 || len(toRemove) > 0 {
		log.Info(fmt.Sprintf("updating albums for image '%s': %d to add, %d to remove", imageId, len(toAdd), len(toRemove)))

		// perform the additions and removals in parallel
		var (
			xrefWg    sync.WaitGroup
			xrefErrCh = make(chan error, len(toAdd)+len(toRemove))
		)

		for _, a := range toAdd {
			xrefWg.Add(1)
			go func(albumId string) {
				defer xrefWg.Done()
				qry := `
				INSERT INTO album_image (
					id, 
					album_uuid, 
					image_uuid, 
					created_at) 
				VALUES (?, ?, ?, ?)`
				xref := db.AlbumImageXref{
					Id:        0, // auto-incremented by the database
					AlbumId:   albumId,
					ImageId:   imageId,
					CreatedAt: data.CustomTime{Time: time.Now().UTC()},
				}
				if err := s.sql.InsertRecord(qry, xref); err != nil {
					xrefErrCh <- fmt.Errorf("failed to add album '%s' to image '%s': %v", albumId, imageId, err)
					return
				}
				log.Info(fmt.Sprintf("added album '%s' to image '%s'", albumId, imageId))
			}(a.Id)
		}

		for _, a := range toRemove {
			xrefWg.Add(1)
			go func(albumId string) {
				defer xrefWg.Done()
				qry := `DELETE FROM album_image WHERE album_uuid = ? AND image_uuid = ?`
				if err := s.sql.DeleteRecord(qry, albumId, imageId); err != nil {
					xrefErrCh <- fmt.Errorf("failed to remove album '%s' from image '%s': %v", albumId, imageId, err)
					return
				}
				log.Info(fmt.Sprintf("removed album '%s' from image '%s'", albumId, imageId))
			}(a.Id)
		}

		xrefWg.Wait()
		close(xrefErrCh)

		// check for errors during xref updates
		if len(xrefErrCh) > 0 {
			var errs []error
			for e := range xrefErrCh {
				errs = append(errs, e)
			}
			if len(errs) > 0 {
				return fmt.Errorf("failed to update some album_image xref records: %v", errors.Join(errs...))
			}
		}

		log.Info(fmt.Sprintf("successfully updated albums for image '%s'", imageId))
	} else {
		log.Info(fmt.Sprintf("no album changes detected for image '%s', nothing to update", imageId))
	}

	return nil
}
