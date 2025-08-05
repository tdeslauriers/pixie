package album

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/permission"
)

// Service is an interface for methods that manage album records.
type Service interface {

	// GetAllowedAlbums returns a list of albums that the user is allowed to view based on their permissions.
	GetAllowedAlbums(username string) ([]AlbumRecord, error)

	// CreateAlbum creates a new album record in the database, ecrypots sensitive fields, and
	// returns a pointer to the created album record,
	// or returns an error if the creation fails.
	CreateAlbum(album AddAlbumCmd) (*AlbumRecord, error)
}

// NewService creates a new album service and provides a pointer to a concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &service{
		sql:     sql,
		indexer: i,
		cryptor: c,
		perms:   permission.NewService(sql, i, c),

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
	perms   permission.Service

	logger *slog.Logger
}

// GetAllowedAlbums implements the Service interface method to retrieve all album records a user has permission to view.
// this method must consider the users permissions, and
// the images attached to the albums premissions and only return an album if the user is
// authorized to view at least one image in the album.
func (s *service) GetAllowedAlbums(username string) ([]AlbumRecord, error) {

	// validate the username
	// redundant check, but good practice
	if err := validate.IsValidEmail(username); err != nil {
		return nil, fmt.Errorf("invalid username: %v", err)
	}

	// fetch a map of the user's permissions for query builder
	psMap, _, err := s.perms.GetPatronPermissions(username)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve permissions for user '%s': %v", username, err)
	}

	// build album query based on permissions
	qry, err := buildAlbumPermisssionQuery(psMap)
	if err != nil {
		return nil, fmt.Errorf("failed to build album permission query: %v", err)
	}

	// convert the permissions map into a variatic slice of interface{}, ie args ...interface{}
	args := make([]interface{}, 0, len(psMap))
	for _, p := range psMap {
		args = append(args, p.Id)
	}

	var albums []AlbumRecord
	if err := s.sql.SelectRecords(qry, &albums, args...); err != nil {
		if err == sql.ErrNoRows {
			return []AlbumRecord{}, nil
		} else {
			return nil, fmt.Errorf("failed to retrieve albums for user '%s': %v", username, err)
		}
	}

	// decrypt the album records
	var wg sync.WaitGroup
	errCh := make(chan error, len(albums))

	for i := range albums {
		wg.Add(1)
		go func(a *AlbumRecord) {
			defer wg.Done()
			if err := s.decryptAlbumRecord(a); err != nil {
				errCh <- fmt.Errorf("failed to decrypt album record '%s': %v", a.Id, err)
			}
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
			return nil, fmt.Errorf("failed to decrypt one or more album records: %v", errors.Join(errs...))
		}
	}

	return albums, nil
}

// CreateAlbum implements the Service interface method to create a new album record in the database.
func (s *service) CreateAlbum(cmd AddAlbumCmd) (*AlbumRecord, error) {

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

	album := &AlbumRecord{
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
	if err := s.encryptAlbumRecord(album); err != nil {
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

// EncryptAlbum Record encrypts the sensitive fields in the album record.
func (s *service) encryptAlbumRecord(album *AlbumRecord) error {

	if album == nil {
		return fmt.Errorf("album record cannot be nil")
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
			return fmt.Errorf("failed to encrypt album record: %v", errors.Join(errs...))
		}
	}

	// set the encrypted fields in the album record
	album.Title = <-titleCh
	album.Description = <-descriptionCh
	album.Slug = <-slugCh

	return nil
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

// decryptAlbumRecord decrypts the sensitive fields in the album record.

func (s *service) decryptAlbumRecord(album *AlbumRecord) error {
	if album == nil {
		return fmt.Errorf("album record cannot be nil")
	}

	var (
		wg            sync.WaitGroup
		titleCh       = make(chan string, 1)
		descriptionCh = make(chan string, 1)
		slugCh        = make(chan string, 1)
		errCh         = make(chan error, 3)
	)

	wg.Add(3)
	go s.decrypt("album title", album.Title, titleCh, errCh, &wg)
	go s.decrypt("album description", album.Description, descriptionCh, errCh, &wg)
	go s.decrypt("album slug", album.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(titleCh)
	close(descriptionCh)
	close(slugCh)
	close(errCh)

	// check for errors during decryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to decrypt album record: %v", errors.Join(errs...))
		}
	}

	// set the decrypted fields in the album record
	album.Title = <-titleCh
	album.Description = <-descriptionCh
	album.Slug = <-slugCh

	// also need to remove the blind index from the album record
	album.SlugIndex = ""

	return nil
}

// decrypt is a helper method that decrypts a field in the album record.
func (s *service) decrypt(field, ciphertext string, fieldCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	if ciphertext == "" {
		errCh <- fmt.Errorf("field '%s' cannot be empty", field)
		return
	}

	// decrypt the field
	decrypted, err := s.cryptor.DecryptServiceData(ciphertext)
	if err != nil {
		errCh <- fmt.Errorf("failed to decrypt '%s' field: %v", field, err)
		return
	}

	fieldCh <- string(decrypted)
}
