package permissions

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/internal/util"
)

type Service interface {
	// GetAllPermissions retrieves all permissions in the database/persistence layer.
	GetAllPermissions() ([]exo.Permission, error)
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
			   service,
			   name,
			   description,
			   created_at,
			   active,
			   slug, 
			FROM permissions`
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
