package permissions

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/internal/util"
)

type Service interface {
	// GetAllPermissions retrieves all permissions in the database/persistence layer.
	GetAllPermissions() ([]exo.Permission, error)

	// CreatePermission creates a new permission in the database/persistence layer.
	CreatePermission(p *Permission) (*Permission, error)
}

// NewService creates a new permissions service and provides a pointer to a concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &permissionsService{
		sql:     sql,
		indexer: i,
		cryptor: c,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.ComponentKey, util.ComponentPermissions)).
			With(slog.String(util.PackageKey, util.PackagePermissions)),
	}
}

var _ Service = (*permissionsService)(nil)

// permissionsService implements the Service interface for managing permissions to gallery data models and images.
type permissionsService struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor data.Cryptor

	logger *slog.Logger
}

// GetAllPermissions implements the Service interface method to retrieve all permissions from the database/persistence layer.
func (s *permissionsService) GetAllPermissions() ([]exo.Permission, error) {

	qry := `SELECT
			   uuid,
			   name,
			   service,
			   description,
			   created_at,
			   active,
			   slug
			FROM permission`
	var ps []exo.Permission
	if err := s.sql.SelectRecords(qry, &ps); err != nil {
		s.logger.Error("Failed to retrieve permissions", slog.Any("error", err))
		return nil, err
	}

	// check if any permissions were found
	// if not, return
	if len(ps) == 0 {
		s.logger.Warn("No permissions found in the database")
		return nil, nil
	}

	// if records found, decrypt sensitive fields: name, description, slug
	var (
		wg     sync.WaitGroup
		pmChan = make(chan exo.Permission, len(ps))
		errs   = make(chan error, len(ps))
	)

	for _, p := range ps {
		wg.Add(1)
		go func(permission exo.Permission) {
			defer wg.Done()

			decrypted, err := s.decryptPermission(permission)
			if err != nil {
				errs <- fmt.Errorf("failed to decrypt permission '%s': %v", permission.Id, err)
				return
			}

			pmChan <- *decrypted
		}(p)
	}

	wg.Wait()
	close(pmChan)
	close(errs)

	// check for errors during decryption
	if len(errs) > 0 {
		var errsList []error
		for e := range errs {
			errsList = append(errsList, e)
		}
		if len(errsList) > 0 {
			return nil, errors.Join(errsList...)
		}
	}

	// collect decrypted permissions
	var permissions []exo.Permission
	for p := range pmChan {
		permissions = append(permissions, p)
	}

	s.logger.Info(fmt.Sprintf("retrieved and decrypted %d permissions from the database", len(permissions)))

	return permissions, nil
}

// decryptPermission is a helper method that decrypts sensitive fields in the permission data model.
func (s *permissionsService) decryptPermission(p exo.Permission) (*exo.Permission, error) {

	var (
		wg     sync.WaitGroup
		nameCh = make(chan string, 1)
		descCh = make(chan string, 1)
		slugCh = make(chan string, 1)
		errCh  = make(chan error, 3)
	)

	wg.Add(3)

	go s.decrypt("name", p.Name, nameCh, errCh, &wg)
	go s.decrypt("description", p.Description, descCh, errCh, &wg)
	go s.decrypt("slug", p.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(nameCh)
	close(descCh)
	close(slugCh)
	close(errCh)

	// check for errors during decryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
	}

	p.Name = <-nameCh
	p.Description = <-descCh
	p.Slug = <-slugCh

	return &p, nil
}

func (s *permissionsService) decrypt(fieldname, encrpyted string, fieldCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	// decrypt service data
	decrypted, err := s.cryptor.DecryptServiceData(encrpyted)
	if err != nil {
		errCh <- fmt.Errorf("failed to decrypt '%s' field: %v", fieldname, err)
	}

	fieldCh <- string(decrypted)
}

// CreatePermission implements the Service interface method to create a new permission in the database/persistence layer.
func (s *permissionsService) CreatePermission(p *Permission) (*Permission, error) {

	// validate the permission
	// redundant check, but but good practice
	if err := p.Validate(); err != nil {
		s.logger.Error("Failed to validate permission", slog.Any("error", err))
		return nil, fmt.Errorf("invalid permission: %v", err)
	}

	// create uuid and set it in the permission record
	id, err := uuid.NewRandom()
	if err != nil {
		s.logger.Error("Failed to generate UUID for permission", slog.Any("error", err))
		return nil, fmt.Errorf("failed to generate UUID for permission: %v", err)
	}
	p.Id = id.String()

	// create created_at timestamp and set it in the permission record
	now := time.Now().UTC()
	p.CreatedAt = data.CustomTime{Time: now}

	// create a slug for the permission and set it in the permission record
	slug, err := uuid.NewRandom()
	if err != nil {
		s.logger.Error("Failed to generate slug for permission", slog.Any("error", err))
		return nil, fmt.Errorf("failed to generate slug for permission: %v", err)
	}
	p.Slug = slug.String()

	// generate a blind index for the slug and set it in the permission record
	index, err := s.indexer.ObtainBlindIndex(p.Slug)
	if err != nil {
		s.logger.Error("Failed to generate blind index for slug", slog.Any("error", err))
		return nil, fmt.Errorf("failed to generate blind index for slug: %v", err)
	}
	p.SlugIndex = index

	// encrypt the sensitive fields in the permission record
	var (
		wg     sync.WaitGroup
		nameCh = make(chan string, 1)
		descCh = make(chan string, 1)
		slugCh = make(chan string, 1)
		errCh  = make(chan error, 3)
	)

	wg.Add(3)
	go s.encrypt("name", p.Name, nameCh, errCh, &wg)
	go s.encrypt("description", p.Description, descCh, errCh, &wg)
	go s.encrypt("slug", p.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(nameCh)
	close(descCh)
	close(slugCh)
	close(errCh)

	// check for errors during encryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("failed to encrypt permission fields: %v", errors.Join(errs...))
		}
	}

	// build encrypted permission record
	encrypted := &Permission{
		Id:          p.Id,
		Name:        <-nameCh,
		Service:     p.Service,
		Description: <-descCh,
		CreatedAt:   p.CreatedAt,
		Active:      p.Active,
		Slug:        <-slugCh,
		SlugIndex:   p.SlugIndex,
	}

	// insert the permission record into the database
	qry := `INSERT INTO permission (
		uuid,
		name,
		service,
		description,
		created_at,
		active,
		slug,
		slug_index
	) VALUES (
		?, ?, ?, ?, ?, ?, ?, ?
	)`
	if err := s.sql.InsertRecord(qry, encrypted); err != nil {
		s.logger.Error("failed to insert permission record into database", slog.Any("error", err))
		return nil, fmt.Errorf("failed to insert permission record into database: %v", err)
	}

	s.logger.Info(fmt.Sprintf("created permission '%s' in the database", encrypted.Id))

	return p, nil
}

// encrypt is a helper method that encrypts sensitive fields in the permission data model.
func (s *permissionsService) encrypt(field, plaintext string, fieldCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	// encrypt service data
	encrypted, err := s.cryptor.EncryptServiceData([]byte(plaintext))
	if err != nil {
		errCh <- fmt.Errorf("failed to encrypt '%s' field: %v", field, err)
		return
	}

	fieldCh <- string(encrypted)
}
