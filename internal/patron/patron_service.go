package patron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/permission"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// PermissionsService is the interface for managing a patrons permissions.
type Service interface {

	// GetByUsername retrieves a patron record by username from the database and
	// performs necessary decryption.
	GetByUsername(ctx context.Context, username string) (*api.Patron, error)

	// CreatePatron creates a new patron record in the database.
	CreatePatron(username string) (*api.Patron, error)

	// GetPatronPermissions retrieves a patron's permissions from the database.
	// NOTE: at this time, this is a wrapper around the permissions service's function with the same name.
	// It is included here for consistency with the interface.
	GetPatronPermissions(ctx context.Context, username string) (map[string]exo.PermissionRecord, []exo.PermissionRecord, error)

	// UpdatePatronPermissions updates a patron's permissions in the database and returns
	// a map of permissions that were added and removed.
	// slugs are the permission slugs to update for the patron.
	UpdatePatronPermissions(ctx context.Context, p *api.Patron, slugs []string) (map[string]exo.PermissionRecord, map[string]exo.PermissionRecord, error)
}

// NewService creates a new Patron service instance, returning a pointer to the concrete implementation.
func NewService(sql *sql.DB, i data.Indexer, c data.Cryptor) Service {
	return &patronService{
		db:          NewPatronRepository(sql),
		indexer:     i,
		cryptor:     c,
		permissions: permission.NewService(sql, i, c),

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePatron)).
			With(slog.String(util.ComponentKey, util.ComponentPatron)),
	}
}

var _ Service = (*patronService)(nil)

type patronService struct {
	db          PatronRepository
	indexer     data.Indexer
	cryptor     data.Cryptor
	permissions permission.Service

	logger *slog.Logger
}

// GetByUsername is the concrete implementation of the interface method which
// retrieves a patron record by username from the database and performs necessary decryption.
func (s *patronService) GetByUsername(ctx context.Context, username string) (*api.Patron, error) {

	// validate the username
	if err := validate.IsValidEmail(username); err != nil {
		return nil, fmt.Errorf("invalid username '%s': %v", username, err)
	}

	// get the user's blind index
	index, err := s.indexer.ObtainBlindIndex(username)
	if err != nil {
		return nil, err
	}

	// query the database for the patron record
	record, err := s.db.FindByIndex(index)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve patron by username '%s': %v", username, err)
	}

	// decrypt the patron's username and slug
	if err := s.decryptPatron(record); err != nil {
		return nil, err
	}

	// get the patron's permissions
	_, ps, err := s.permissions.GetPatronPermissions(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve permissions for patron '%s': %v", username, err)
	}

	return &api.Patron{
		Id:          record.Id,
		Username:    record.Username,
		Slug:        record.Slug,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
		IsArchived:  record.IsArchived,
		IsActive:    record.IsActive,
		Permissions: ps,
	}, nil

}

// decryptPatron is a helper function to decrypt the patron's username and slug.
func (s *patronService) decryptPatron(patron *PatronRecord) error {

	if patron == nil {
		return fmt.Errorf("patron record is nil")
	}

	var (
		wg         sync.WaitGroup
		usernameCh = make(chan string, 1)
		slugCh     = make(chan string, 1)
		errCh      = make(chan error, 2)
	)

	wg.Add(2)
	go s.decryptField("username", patron.Username, usernameCh, errCh, &wg)
	go s.decryptField("slug", patron.Slug, slugCh, errCh, &wg)

	wg.Wait()
	close(usernameCh)
	close(slugCh)
	close(errCh)

	// check for errors during decryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		return fmt.Errorf("failed to decrypt patron record: %s", errors.Join(errs...))
	}

	// update patron record with decrypted values
	patron.Username = <-usernameCh
	patron.Slug = <-slugCh

	return nil
}

// decryptField is a helper method that decrypts a field in the patron record.
func (s *patronService) decryptField(fieldname, fieldvalue string, fieldCh chan<- string, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	if fieldvalue == "" {
		fieldCh <- ""
		return
	}

	decrypted, err := s.cryptor.DecryptServiceData(fieldvalue)
	if err != nil {
		errCh <- fmt.Errorf("failed to decrypt '%s' field of patron record: %v", fieldname, err)
		return
	}

	fieldCh <- string(decrypted)
}

// CreatePatron is the concrete implementation of the interface method which
// creates a new patron record in the database.
func (s *patronService) CreatePatron(username string) (*api.Patron, error) {

	// validate the username
	// redundant check, but good practice
	if err := validate.IsValidEmail(username); err != nil {
		return nil, fmt.Errorf("invalid username '%s': %v", username, err)
	}

	// create the patron record
	// no need for concurrency since this is invisible to users
	// and will not be called frequently

	// generate the patrons uuid
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate uuid for patron: %v", err)
	}

	// encrypt user name
	encryptedUsername, err := s.cryptor.EncryptServiceData([]byte(username))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt username '%s': %v", username, err)
	}

	// generate the user's blind index
	userIndex, err := s.indexer.ObtainBlindIndex(username)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain blind index for username '%s': %v", username, err)
	}

	// generate the slug
	slug, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate slug for patron: %v", err)
	}

	// encrypt the slug
	encryptedSlug, err := s.cryptor.EncryptServiceData([]byte(slug.String()))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt slug '%s': %v", slug, err)
	}

	// generate the slug index
	slugIndex, err := s.indexer.ObtainBlindIndex(slug.String())
	if err != nil {
		return nil, fmt.Errorf("failed to obtain blind index for slug '%s': %v", slug, err)
	}

	now := data.CustomTime{Time: time.Now().UTC()}

	record := PatronRecord{
		Id:         id.String(),
		Username:   string(encryptedUsername),
		UserIndex:  userIndex,
		Slug:       string(encryptedSlug),
		SlugIndex:  slugIndex,
		CreatedAt:  now,
		UpdatedAt:  now,
		IsArchived: false,
		IsActive:   true,
	}

	// insert the patron record into the database
	if err := s.db.InsertPatron(record); err != nil {
		return nil, fmt.Errorf("failed to insert patron record into database: %v", err)
	}

	// return the patron record
	return &api.Patron{
		Id:         record.Id,
		Username:   username,
		Slug:       slug.String(),
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
		IsArchived: record.IsArchived,
		IsActive:   record.IsActive,
	}, nil
}

// GetPatronPermissions is the concrete implementation of the interface method which
// retrieves a patron's permissions from the database.
// NOTE: at this time, this is a wrapper around the permissions service's function with the same name.
// It is included here for consistency with the interface.
func (s *patronService) GetPatronPermissions(ctx context.Context, username string) (map[string]exo.PermissionRecord, []exo.PermissionRecord, error) {

	return s.permissions.GetPatronPermissions(ctx, username)
}

// UpdatePatronPermissions is the concrete implementation of the interface method which
// updates a patron's permissions in the database.
func (s *patronService) UpdatePatronPermissions(ctx context.Context, pat *api.Patron, slugs []string) (map[string]exo.PermissionRecord, map[string]exo.PermissionRecord, error) {

	// create local logger
	// add telemetry from context if exists
	log := s.logger
	if tel, ok := connect.GetTelemetryFromContext(ctx); ok && tel != nil {
		log = s.logger.With(tel.TelemetryFields()...)
	} else {
		log.Warn("no telemetry found in context for UpdatePatronPermissions")
	}

	if pat == nil {
		return nil, nil, fmt.Errorf("patron record is nil")
	}

	// validate the permissions slugs are well formed uuids
	for _, slug := range slugs {
		if !validate.IsValidUuid(slug) {
			return nil, nil, fmt.Errorf("invalid permission slug '%s'", slug)
		}
	}

	// get map of all permissions for slug validation
	all, _, err := s.permissions.GetAllPermissions()
	if err != nil {
		return nil, nil, err
	}

	// build update map of permissions
	// return an error if any slug is not found in the permissions map
	// key is the permission record's slug
	updated := make(map[string]exo.PermissionRecord, len(slugs))
	for _, slug := range slugs {
		if p, ok := all[slug]; ok {
			updated[slug] = p
		} else {
			return nil, nil, fmt.Errorf("update permission slug '%s' not found", slug)
		}
	}

	// build current map of permissions
	// key is the permission slug
	current := make(map[string]exo.PermissionRecord, len(pat.Permissions))
	for _, pm := range pat.Permissions {
		current[pm.Slug] = pm
	}

	// build map of permissions to add to the patron
	toAdd := make(map[string]exo.PermissionRecord, len(updated))
	for slug, pm := range updated {
		if _, exists := current[slug]; !exists {
			toAdd[slug] = pm
		}
	}

	// build map of permissions to remove from the patron
	toRemove := make(map[string]exo.PermissionRecord, len(current))
	for slug, pm := range current {
		if _, exists := updated[slug]; !exists {
			toRemove[slug] = pm
		}
	}

	// return early if there are no changes to be made
	if len(toAdd) == 0 && len(toRemove) == 0 {
		s.logger.Info(fmt.Sprintf("no changes to permissions for patron '%s'", pat.Username))
		return nil, nil, nil
	}

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, len(toAdd)+len(toRemove))
	)

	// add new permissions to the patron if applicable
	if len(toAdd) > 0 {
		for _, pm := range toAdd {
			wg.Add(1)
			go func(permission exo.PermissionRecord, eCh chan<- error, wg *sync.WaitGroup) {
				defer wg.Done()

				if err := s.permissions.AddPermissionToPatron(pat.Id, permission.Id); err != nil {
					eCh <- fmt.Errorf("failed to add permission '%s' to patron '%s': %v", permission.Slug, pat.Username, err)
					return
				}

				log.Info(fmt.Sprintf("permission '%s' added to patron '%s'", permission.Name, pat.Username))
			}(pm, errCh, &wg)
		}
	}

	// remove permissions from the patron if applicable
	if len(toRemove) > 0 {
		for _, pm := range toRemove {
			wg.Add(1)
			go func(permission exo.PermissionRecord, eCh chan<- error, wg *sync.WaitGroup) {
				defer wg.Done()

				if err := s.permissions.RemovePermissionFromPatron(pat.Id, permission.Id); err != nil {
					eCh <- fmt.Errorf("failed to remove permission '%s' from patron '%s': %v", permission.Slug, pat.Username, err)
					return
				}

				log.Info(fmt.Sprintf("permission '%s' removed from patron '%s'", permission.Name, pat.Username))
			}(pm, errCh, &wg)
		}
	}

	wg.Wait()
	close(errCh)

	// check for errors during permission updates
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return nil, nil, fmt.Errorf("failed to update permissions for patron '%s': %s", pat.Username, errors.Join(errs...))
		}
	}

	return toAdd, toRemove, nil
}
