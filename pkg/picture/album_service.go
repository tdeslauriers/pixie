package picture

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
	"github.com/tdeslauriers/pixie/pkg/crypt"
)

// AlbumService is an interface for methods that manage album records.
type AlbumService interface {

	// GetAllowedAlbums returns a map ([slug]album) and a list of albums that the user is allowed to
	// view based on their permissions.
	// Map uses slug since that is the most likely value to be used for lookups.
	GetAllowedAlbums(psMap map[string]permissions.PermissionRecord) (map[string]db.AlbumRecord, []db.AlbumRecord, error)

	// GetAlbum returns a specific album record by its slug, and
	// a slice of the thumbnail images for the album.
	// Note: username is required to check permissions for each of the album's associated images.
	GetAlbumBySlug(slug string, psMap map[string]permissions.PermissionRecord) (*api.Album, error)

	// CreateAlbum creates a new album record in the database, ecrypots sensitive fields, and
	// returns a pointer to the created album record,
	// or returns an error if the creation fails.
	CreateAlbum(album api.AddAlbumCmd) (*db.AlbumRecord, error)

	// UpdateAlbum updates an existing album record in the database.
	UpdateAlbum(updated db.AlbumRecord) error

	// InsertAlbumImageXref creates a new record in the album_image xref table to associate an image with an album.
	InsertAlbumImageXref(albumId, imageId string) error
}

// NewAlbumService creates a new album service and provides a pointer to a concrete implementation.
func NewAlbumService(sql data.SqlRepository, i data.Indexer, c data.Cryptor, o storage.ObjectStorage) AlbumService {
	return &albumService{
		sql:     sql,
		indexer: i,
		cryptor: crypt.NewCryptor(c),
		store:   o,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumSerivce)).
			With(slog.String(util.PackageKey, util.PackagePicture)),
	}
}

var _ AlbumService = (*albumService)(nil)

// albumService implements the Service interface for managing album records.
type albumService struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor crypt.Cryptor
	store   storage.ObjectStorage

	logger *slog.Logger
}

// GetAllowedAlbums implements the Service interface method to retrieve all album records a user has permission to view.
// this method must consider the users permissions, and
// the images attached to the albums premissions and only return an album if the user is
// authorized to view at least one image in the album.
// Also returns a map for convenience lookups by consuming functions.
func (s *albumService) GetAllowedAlbums(psMap map[string]permissions.PermissionRecord) (map[string]db.AlbumRecord, []db.AlbumRecord, error) {

	// build album query based on permissions
	qry, err := db.BuildAlbumSQuery(psMap)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build album permission query: %v", err)
	}

	// convert the permissions map into a variatic slice of interface{}, ie args ...interface{}
	args := make([]interface{}, 0, len(psMap))
	// if user is curator, no need to filter by permissions
	if _, ok := psMap["CURATOR"]; !ok {
		for _, p := range psMap {
			args = append(args, p.Id)
		}
	}

	var albums []db.AlbumRecord
	if err := s.sql.SelectRecords(qry, &albums, args...); err != nil {
		if err == sql.ErrNoRows {
			return nil, []db.AlbumRecord{}, nil
		} else {
			return nil, nil, err
		}
	}

	// decrypt the album records
	var wg sync.WaitGroup
	errCh := make(chan error, len(albums))

	for i := range albums {
		wg.Add(1)
		go func(a *db.AlbumRecord) {
			defer wg.Done()
			if err := s.cryptor.DecryptAlbumRecord(a); err != nil {
				errCh <- fmt.Errorf("failed to decrypt album record '%s': %v", a.Id, err)
			}
			// also need to remove the blind index from the album record
			a.SlugIndex = ""
		}(&albums[i])
	}

	wg.Wait()
	close(errCh)

	// return errors if any decryption failed
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, nil, fmt.Errorf("failed to decrypt one or more album records: %v", errors.Join(errs...))
		}
	}

	// after decryption, build a lookup map for convenience
	aMap := make(map[string]db.AlbumRecord, len(albums))
	for _, a := range albums {
		aMap[a.Slug] = a
	}

	return aMap, albums, nil
}

// GetAlbumBySlug implements the Service interface method to retrieve a specific album record by its slug.
// It also retrieves a slice of the thumbnail images for the album.
func (s *albumService) GetAlbumBySlug(slug string, psMap map[string]permissions.PermissionRecord) (*api.Album, error) {

	// vadidate the slug is well formed
	// redundant check, but good practice
	if !validate.IsValidUuid(slug) {
		return nil, fmt.Errorf("invalid album slug: %s", slug)
	}

	// get album index
	slugIndex, err := s.indexer.ObtainBlindIndex(slug)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain blind index for album slug '%s': %v", slug, err)
	}

	// build the album query with the users permissions
	qry, err := db.BuildAlbumImagesQuery(psMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create query for album slug %s", slug)
	}

	// convert the permissions map into a variatic slice of interface{}, ie args ...interface{}
	args := make([]interface{}, 0, len(psMap)+1) // capacity needs to include the slug index
	args = append(args, slugIndex)               // index in first args position
	// if user is curator, no need to filter by permissions
	if _, ok := psMap["CURATOR"]; !ok {
		// append the permissions uuids to the args slice
		for _, p := range psMap {
			args = append(args, p.Id)
		}
	}

	var records []db.AlbumImageRecord
	if err := s.sql.SelectRecords(qry, &records, args...); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("failed to find images user has permission to view in this album")
		} else {
			return nil, fmt.Errorf("failed to retrieve album slug %s from database for user: %v", slug, err)
		}
	}

	// seocondary check to ensure we have records
	if len(records) == 0 {
		return nil, fmt.Errorf("no images found for album slug %s and user's permissions level(s)", slug)
	}

	// build the album modelfrom the first record
	album := &api.Album{
		Id:          records[0].AlbumId,
		Title:       records[0].AlbumTitle,
		Description: records[0].AlbumDescription,
		Slug:        records[0].AlbumSlug,
		CreatedAt:   records[0].AlbumCreatedAt,
		UpdatedAt:   records[0].AlbumUpdatedAt,
		IsArchived:  records[0].AlbumIsArchived,
		// Images slice will be populated below after additional operations
	}

	// decrypt the album record
	if err := s.cryptor.DecryptAlbum(album); err != nil {
		return nil, fmt.Errorf("failed to decrypt album record '%s': %v", album.Id, err)
	}

	// build the images slice from the records
	// includes decryption of sensitive fields and
	// getting presigned links to the image thumbnails
	images, err := s.buildImageData(records)
	if err != nil {
		return nil, fmt.Errorf("failed to build image data for album slug %s: %v", slug, err)
	}

	// set the images slice on the album
	album.Images = images

	return album, nil
}

// buildImageData takes the fields from a AlbumImageRecords and builds an slice of decrypted ImageData structs
// with presigned URLs for the thumbnail images.
func (s *albumService) buildImageData(records []db.AlbumImageRecord) ([]api.ImageData, error) {

	// chack that records slice is not empty
	if len(records) == 0 {
		return []api.ImageData{}, nil
	}

	// build the image data slice
	images := make([]api.ImageData, len(records))

	var wg sync.WaitGroup
	errCh := make(chan error, len(records))

	for i, r := range records {
		wg.Add(1)
		go func(i int, r db.AlbumImageRecord) {
			defer wg.Done()

			// build the image data struct
			img := &api.ImageData{
				Id:          r.ImageId,
				Title:       r.ImageTitle,
				Description: r.ImageDescription,
				FileName:    r.FileName,
				ObjectKey:   r.ObjectKey,
				Slug:        r.ImageSlug,
				ImageDate:   r.ImageDate,
				CreatedAt:   r.ImageCreatedAt,
				UpdatedAt:   r.ImageUpdatedAt,
				IsArchived:  r.ImageIsArchived,
				IsPublished: r.ImageIsPublished,
			}

			// possible all fields will be empty if no images are attached to the album
			// if the id is empty, very likely all fields are empty
			if img.Id == "" {
				s.logger.Warn(fmt.Sprintf("image index[%i] fields are empty for album %s", r.AlbumTitle))
				return
			}

			// decrypt the sensitive fields in the image data
			if err := s.cryptor.DecryptImageData(img); err != nil {
				errCh <- fmt.Errorf("failed to decrypt image data '%s': %v", img.Id, err)
				return
			}

			// get a presigned URL for the thumbnail image
			url, err := s.store.GetSignedUrl(fmt.Sprintf("%s_thumbnail", img.ObjectKey))
			if err != nil {
				if strings.Contains(err.Error(), "does not exist") {
					s.logger.Warn(fmt.Sprintf("thumbnail image for image '%s', filename '%s' does not exist in object storage", img.Id, img.FileName))
				} else {
					errCh <- fmt.Errorf("failed to get presigned URL for image '%s', filename '%s' record's thumbnail: %v", img.Id, img.FileName, err)
					return
				}
			}

			// if the url is empty, skip setting it on the image data
			if url != nil {
				// set the signed thumbnail URL in the image data
				img.SignedUrl = url.String()
			}

			// set the image data in the slice
			images[i] = *img

		}(i, r)
	}

	wg.Wait()
	close(errCh)

	// check for errors during decryption and URL generation
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("failed to build image data for album: %v", errors.Join(errs...))
		}
	}

	// return the images slice
	return images, nil
}

// CreateAlbum implements the Service interface method to create a new album record in the database.
func (s *albumService) CreateAlbum(cmd api.AddAlbumCmd) (*db.AlbumRecord, error) {

	// validate the album add cmd
	// redundant check, but good practice
	if err := cmd.Validate(); err != nil {
		return nil, err
	}

	// build the album record
	// create uuid
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate uuid for album record: %v", err)
	}

	// generate the slug
	slug, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate slug for album record: %v", err)
	}

	// generate the slug index
	slugIndex, err := s.indexer.ObtainBlindIndex(slug.String())
	if err != nil {
		return nil, fmt.Errorf("failed to generate blind index for album slug '%s': %v", slug.String(), err)
	}

	// set created and updated timestamps
	now := time.Now().UTC()

	// build  album record

	album := &db.AlbumRecord{
		Id:          id.String(),
		Title:       cmd.Title,
		Description: cmd.Description,
		Slug:        slug.String(),
		SlugIndex:   slugIndex,
		CreatedAt:   data.CustomTime{Time: now},
		UpdatedAt:   data.CustomTime{Time: now},
		IsArchived:  cmd.IsArchived,
	}
	// encrypt the sensitive fields in the album record
	if err := s.cryptor.EncryptAlbumRecord(album); err != nil {
		return nil, err
	}

	// insert the encrypted album record into the database
	qry := `
		INSERT INTO album (
			uuid,
			title,
			description,
			slug,
			slug_index,
			created_at,
			updated_at,
			is_archived
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if err := s.sql.InsertRecord(qry, *album); err != nil {
		return nil, fmt.Errorf("failed to insert album record into database: %v", err)
	}

	// log the creation
	s.logger.Info(fmt.Sprintf("created album record '%s'", album.Title))

	// set the plaintext title and description back on the album record
	// so that it can be returned to the caller in readable format
	album.Title = cmd.Title
	album.Description = cmd.Description
	album.Slug = slug.String()

	return album, nil
}

// UpdateAlbum updates an existing album record in the database.
func (s *albumService) UpdateAlbum(updated db.AlbumRecord) error {

	// validate the updated album record
	// redundant check, but good practice
	if err := updated.Validate(); err != nil {
		return fmt.Errorf("invalid updated album record: %v", err)
	}

	// build blind index for the slug
	slugIndex, err := s.indexer.ObtainBlindIndex(updated.Slug)
	if err != nil {
		return fmt.Errorf("failed to generate blind index for album slug '%s': %v", updated.Slug, err)
	}

	// encrypt the sensitive fields in the album record
	// dont need to create a copy because passed in as value
	if err := s.cryptor.EncryptAlbumRecord(&updated); err != nil {
		return err
	}

	// build the update query
	qry := `
		UPDATE album
		SET
			title = ?,
			description = ?,
			is_archived = ?,
			updated_at = ?
		WHERE slug_index = ?`
	if err := s.sql.UpdateRecord(
		qry,
		updated.Title,
		updated.Description,
		updated.IsArchived,
		updated.UpdatedAt,
		slugIndex,
	); err != nil {
		return fmt.Errorf("failed to update album record '%s': %v", updated.Id, err)
	}

	// log the update
	s.logger.Info(fmt.Sprintf("updated album record '%s'", updated.Id))

	return nil
}

func (s *albumService) InsertAlbumImageXref(albumId, imageId string) error {

	// validate the album id and image id
	// redundant check, but good practice
	if !validate.IsValidUuid(albumId) {
		return fmt.Errorf("invalid album id: %s", albumId)
	}
	if !validate.IsValidUuid(imageId) {
		return fmt.Errorf("invalid image id: %s", imageId)
	}

	// build the xref record
	xref := db.AlbumImageXref{
		Id:        0, // auto-increment
		AlbumId:   albumId,
		ImageId:   imageId,
		CreatedAt: data.CustomTime{Time: time.Now().UTC()},
	}

	qry := `
		INSERT INTO album_image (
			id,
			album_uuid,
			image_uuid,
			created_at
		) VALUES (?, ?, ?, ?)`
	if err := s.sql.InsertRecord(qry, xref); err != nil {
		return fmt.Errorf("failed to insert album-image xref record into database: %v", err)
	}

	return nil
}
