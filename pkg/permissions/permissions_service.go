package permissions

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
)

type Service interface {
	// GetAllPermissions retrieves all permissions in the database/persistence layer.
	GetAllPermissions() ([]PermissionRecord, error)

	// GetPermissionBySlug retrieves a permission by its slug from the database/persistence layer.
	GetPermissionBySlug(slug string) (*PermissionRecord, error)

	// CreatePermission creates a new permission in the database/persistence layer.
	CreatePermission(p *PermissionRecord) (*PermissionRecord, error)

	// UpdatePermission updates an existing permission in the database/persistence layer.
	// or returns an error if the update fails.
	UpdatePermission(p *PermissionRecord) error
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
func (s *permissionsService) GetAllPermissions() ([]PermissionRecord, error) {

	qry := `SELECT
			   uuid,
			   service_name,
			   permission,
			   name,
			   description,
			   created_at,
			   active,
			   slug,
			   slug_index
			FROM permission`
	var ps []PermissionRecord
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

	// if records found, decrypt sensitive fields: name, permission, description, slug
	var (
		wg     sync.WaitGroup
		pmChan = make(chan PermissionRecord, len(ps))
		errs   = make(chan error, len(ps))
	)

	for _, p := range ps {
		wg.Add(1)
		go func(permission PermissionRecord) {
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
	var permissions []PermissionRecord
	for p := range pmChan {
		permissions = append(permissions, p)
	}

	s.logger.Info(fmt.Sprintf("retrieved and decrypted %d permission(s) from the database", len(permissions)))

	return permissions, nil
}

// GetPermissionBySlug implements the Service interface method to retrieve a permission by its slug from the database/persistence layer.
func (s *permissionsService) GetPermissionBySlug(slug string) (*PermissionRecord, error) {

	// validate slug
	// redundant check, but good practice
	if valid := validate.IsValidUuid(slug); !valid {
		s.logger.Error("Invalid slug provided", slog.String("slug", slug))
		return nil, fmt.Errorf("invalid slug: %s", slug)
	}

	// get blind index for the slug
	index, err := s.indexer.ObtainBlindIndex(slug)
	if err != nil {
		s.logger.Error("Failed to generate blind index for slug", slog.Any("error", err))
		return nil, fmt.Errorf("failed to generate blind index for slug: %v", err)
	}

	// query to retrieve the permission by slug index
	qry := `
		SELECT
			uuid,
			service_name,
			permission,
			name,
			description,
			created_at,
			active,
			slug,
			slug_index
		FROM permission
		WHERE slug_index = ?`
	var p PermissionRecord
	if err := s.sql.SelectRecord(qry, &p, index); err != nil {
		if err == sql.ErrNoRows {
			s.logger.Error(fmt.Sprintf("No permission found for slug '%s'", slug))
			return nil, fmt.Errorf("no permission found for slug '%s'", slug)
		} else {
			s.logger.Error(fmt.Sprintf("Failed to retrieve permission by slug '%s': %v", slug, err))
			return nil, fmt.Errorf("failed to retrieve permission by slug '%s': %v", slug, err)
		}
	}

	// prepare the permission by decrypting sensitive fields and removing unnecessary fields
	prepared, err := s.decryptPermission(p)
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to prepare permission '%s': %v", slug, err))
		return nil, fmt.Errorf("failed to prepare permission 'slug %s': %v", slug, err)
	}

	return prepared, nil
}

// decryptPermission is a helper method that decrypts sensitive fields  and removes uncessary fields in the permission data model.
func (s *permissionsService) decryptPermission(p PermissionRecord) (*PermissionRecord, error) {

	var (
		wg     sync.WaitGroup
		pmCh   = make(chan string, 1)
		nameCh = make(chan string, 1)
		descCh = make(chan string, 1)
		slugCh = make(chan string, 1)
		errCh  = make(chan error, 4)
	)

	wg.Add(4)
	go s.decrypt("permission", p.Permission, pmCh, errCh, &wg)
	go s.decrypt("name", p.Name, nameCh, errCh, &wg)
	go s.decrypt("description", p.Description, descCh, errCh, &wg)
	go s.decrypt("slug", p.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(pmCh)
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

	p.Permission = <-pmCh
	p.Name = <-nameCh
	p.Description = <-descCh
	p.Slug = <-slugCh
	p.SlugIndex = "" // clear slug index as it is not needed in the response

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
func (s *permissionsService) CreatePermission(p *PermissionRecord) (*PermissionRecord, error) {

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
	encrypted, err := s.encryptPermission(p)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to encrypt permission '%s': %v", p.Id, err))
		return nil, fmt.Errorf("failed to encrypt permission '%s': %v", p.Id, err)
	}

	// insert the permission record into the database
	qry := `INSERT INTO permission (
		uuid,
		service_name,
		permission,
		name,
		description,
		created_at,
		active,
		slug,
		slug_index
	) VALUES (
		?, ?, ?, ?, ?, ?, ?, ?, ?
	)`
	if err := s.sql.InsertRecord(qry, *encrypted); err != nil {
		s.logger.Error("failed to insert permission record into database", slog.Any("error", err))
		return nil, fmt.Errorf("failed to insert permission record into database: %v", err)
	}

	s.logger.Info(fmt.Sprintf("created permission '%s' in the database", encrypted.Id))

	// return unencrypted permission record
	// remove slug index as it is not needed in the response
	p.SlugIndex = "" // clear slug index as it is not needed in the response

	return p, nil
}

// UpdatePermission implements the Service interface method to update an existing permission in the database/persistence layer.
func (s *permissionsService) UpdatePermission(p *PermissionRecord) error {

	// validate the permission
	// redundant check, but good practice
	if err := p.Validate(); err != nil {
		s.logger.Error("Failed to validate permission", slog.Any("error", err))
		return fmt.Errorf("invalid permission: %v", err)
	}

	// get the blind index for the slug
	index, err := s.indexer.ObtainBlindIndex(p.Slug)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to generate blind index for slug '%s': %v", p.Slug, err))
		return fmt.Errorf("failed to generate blind index for slug '%s': %v", p.Slug, err)
	}

	// encrypt fields for persisting the updated permission
	encrypted, err := s.encryptPermission(p)
	if err != nil {
		s.logger.Error(fmt.Sprintf("Failed to encrypt permission '%s': %v", p.Id, err))
		return fmt.Errorf("failed to encrypt permission '%s': %v", p.Id, err)
	}

	// update the permission record in the database
	qry := `
		UPDATE permission SET
			permission = ?,
			name = ?,
			description = ?,
			active = ?,
			slug = ?
		WHERE slug_index = ?`
	if err := s.sql.UpdateRecord(qry, encrypted.Permission, encrypted.Name, encrypted.Description, encrypted.Active, encrypted.Slug, index); err != nil {
		s.logger.Error(fmt.Sprintf("failed to update permission '%s - %s' in the database: %v", p.Id, p.Name, err))
		return fmt.Errorf("failed to update permission '%s' in the database: %v", p.Name, err)
	}

	s.logger.Info(fmt.Sprintf("updated permission '%s' in the database", p.Id))
	return nil
}

// encryptPermission is a helper method that encrypts sensitive fields
// in the permission data model, preparing the record for storage in the database.
func (s *permissionsService) encryptPermission(p *PermissionRecord) (*PermissionRecord, error) {

	var (
		wg     sync.WaitGroup
		pmCh   = make(chan string, 1)
		nameCh = make(chan string, 1)
		descCh = make(chan string, 1)
		slugCh = make(chan string, 1)
		errCh  = make(chan error, 4)
	)

	wg.Add(4)
	go s.encrypt("permission", p.Permission, pmCh, errCh, &wg)
	go s.encrypt("name", p.Name, nameCh, errCh, &wg)
	go s.encrypt("description", p.Description, descCh, errCh, &wg)
	go s.encrypt("slug", p.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(pmCh)
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
			return nil, errors.Join(errs...)
		}
	}

	encrypted := &PermissionRecord{
		Id:          p.Id,
		ServiceName: p.ServiceName,
		Permission:  <-pmCh,
		Name:        <-nameCh,
		Description: <-descCh,
		CreatedAt:   p.CreatedAt,
		Active:      p.Active,
		Slug:        <-slugCh,
		SlugIndex:   p.SlugIndex, // slug index is not encrypted, is hash
	}

	return encrypted, nil
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
