package album

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/internal/util"
)

// Service is an interface for methods that manage album records.
type Service interface {

	// CreateAlbum creates a new album record in the database, ecrypots sensitive fields, and
	// returns a pointer to the created album record,
	// or returns an error if the creation fails.
	CreateAlbum(album *AlbumRecord) (*AlbumRecord, error)
}

// NewService creates a new album service and provides a pointer to a concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &service{
		sql:     sql,
		indexer: i,
		cryptor: c,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumSerivce)).
			With(slog.String(util.PackageKey, util.PackageAlbum)),
	}
}

var _ Service = (*service)(nil)

// service implements the Service interface for managing album records.
type service struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor data.Cryptor

	logger *slog.Logger
}

// CreateAlbum implements the Service interface method to create a new album record in the database.
func (s *service) CreateAlbum(album *AlbumRecord) (*AlbumRecord, error) {

	// validate the album record
	// redundant check, but good practice
	if err := album.Validate(); err != nil {
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

	// set the unpopulated fields in the album record -> that would not have come in from the client
	album.Id = id.String()
	album.Slug = slug.String()
	album.SlugIndex = slugIndex
	album.CreatedAt = data.CustomTime{Time: now}
	album.UpdatedAt = data.CustomTime{Time: now}

	// encrypt the sensitive fields in the album record
	encrypted, err := s.EncryptAlbumRecord(album)
	if err != nil {
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
	if err := s.sql.InsertRecord(qry, *encrypted); err != nil {
		return nil, fmt.Errorf("failed to insert album record into database: %v", err)
	}

	s.logger.Info(fmt.Sprintf("created album record '%s'", album.Title))

	return album, nil
}

// EncryptAlbum Record encrypts the sensitive fields in the album record.
func (s *service) EncryptAlbumRecord(album *AlbumRecord) (*AlbumRecord, error) {

	if album == nil {
		return nil, fmt.Errorf("album record cannot be nil")
	}

	var (
		wg            sync.WaitGroup
		titleCh       = make(chan string, 1)
		descriptionCh = make(chan string, 1)
		slugCh        = make(chan string, 1)
		errCh         = make(chan error, 3)
	)

	wg.Add(3)
	go s.encrypt("album title", album.Title, titleCh, errCh, &wg)
	go s.encrypt("album description", album.Description, descriptionCh, errCh, &wg)
	go s.encrypt("album slug", album.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(titleCh)
	close(descriptionCh)
	close(slugCh)
	close(errCh)

	// check for errors during encryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("failed to encrypt album record: %v", errors.Join(errs...))
		}
	}

	// set the encrypted fields in the album record
	album.Title = <-titleCh
	album.Description = <-descriptionCh
	album.Slug = <-slugCh

	return album, nil
}

// encrypt is a helper method that encrypts a field in the album record.

func (s *service) encrypt(field, plaintext string, fieldCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	if plaintext == "" {
		errCh <- fmt.Errorf("field '%s' cannot be empty", field)
		return
	}

	// encrypt the field
	encrypted, err := s.cryptor.EncryptServiceData([]byte(plaintext))
	if err != nil {
		errCh <- fmt.Errorf("failed to encrypt '%s' field '%s': %v", field, plaintext, err)
		return
	}

	fieldCh <- encrypted
}
