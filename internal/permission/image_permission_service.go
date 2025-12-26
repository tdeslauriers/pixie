package permission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
)

// ImagePermissionService is an interface for managing image_permisssion xref records in the database.
type ImagePermissionService interface {

	// GetImagePermissions retrieves the permissions associated with an image.
	// Returns a map and slice of PermissionRecords, or an error if any.
	// Key is the permission field, value is the PermissionRecord.
	GetImagePermissions(imageId string) (map[string]exo.PermissionRecord, []exo.PermissionRecord, error)

	// UpdateImagePermissions updates the permissions associated with an image.
	// It adds new permissions and removes old ones as necessary.
	// It takes the image ID and a slice of permission slugs to be associated with the image.
	// Returns an error if any.
	UpdateImagePermissions(ctx context.Context, imageId string, permissionSlugs []string) error
}

// NewImagePermissionService creates a new ImagePermissionService instance, returning a pointer to the concrete implementation.
func NewImagePermissionService(sql *sql.DB, i data.Indexer, c data.Cryptor) ImagePermissionService {
	return &imagePermissionService{
		sql:     NewRepository(sql),
		indexer: i,
		cryptor: exo.NewPermissionCryptor(c),

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePermissions)).
			With(slog.String(util.ComponentKey, util.ComponentImagePermissions)),
	}
}

var _ ImagePermissionService = (*imagePermissionService)(nil)

// imagePermissionService is the concrete implementation of the ImagePermissionService interface.
type imagePermissionService struct {
	sql     Repository
	indexer data.Indexer
	cryptor exo.PermissionCryptor

	logger *slog.Logger
}

// GetImagePermissions is the concrete impl of the interface imple which
// retrieves the permissions associated with an image.
// Returns a map and slice of PermissionRecords, or an error if any.
// Key is the permission field, value is the PermissionRecord.
func (s *imagePermissionService) GetImagePermissions(imageId string) (map[string]exo.PermissionRecord, []exo.PermissionRecord, error) {

	// validate the image id
	if !validate.IsValidUuid(imageId) {
		return nil, nil, fmt.Errorf("image Id must be a valid UUID")
	}

	// build the query to get the permissions associated with the image
	records, err := s.sql.FindImagePermissions(imageId)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to retrieve image '%s' permissions from database: %w", imageId, err)
	}

	if len(records) == 0 {
		return nil, nil, fmt.Errorf("no permissions found for image '%s'", imageId)
	}

	// decrypt and create a map of permissions
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		errCh = make(chan error, len(records))
	)

	// opportunistically map the permssions during decryption
	psMap := make(map[string]exo.PermissionRecord, len(records))
	for i, record := range records {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			decrypted, err := s.cryptor.DecryptPermission(record)
			if err != nil {
				errCh <- fmt.Errorf("failed to decrypt permission '%s': %v", record.Id, err)
				return
			}

			records[i] = *decrypted

			mu.Lock()
			psMap[decrypted.Permission] = *decrypted
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
			return nil, nil, fmt.Errorf("failed to decrypt permission records: %v", errors.Join(errs...))
		}
	}

	return psMap, records, nil
}

// UpdateImagePermissions is the concret implementation of the interface method which
// updates the permissions associated with an image.
// It adds new permissions and removes old ones as necessary.
// It takes the image ID and a slice of permission slugs to be associated with the image.
// Returns an error if any.
func (s *imagePermissionService) UpdateImagePermissions(ctx context.Context, imageId string, permissionSlugs []string) error {

	log := s.logger
	if tel, ok := connect.GetTelemetryFromContext(ctx); ok && tel != nil {
		log = s.logger.With(tel.TelemetryFields()...)
	} else {
		log.Warn("no telemetry found in context for UpdateImagePermissions")
	}

	// validate the image id
	if !validate.IsValidUuid(imageId) {
		return fmt.Errorf("image Id must be a valid UUID")
	}

	// validate the permission slugs
	for _, slug := range permissionSlugs {
		if !validate.IsValidUuid(slug) {
			return fmt.Errorf("permission slug '%s' is not valid", slug)
		}
	}

	// retrieve all permissions from the database
	records, err := s.sql.FindAllPermissions()
	if err != nil {
		return fmt.Errorf("failed to retrieve all permissions: %v", err)
	}

	// this should never happen, but just in case
	if len(records) == 0 {
		return fmt.Errorf("no permissions found in the database")
	}

	// decrypt and create a map of all permissions
	var (
		decryptWg    sync.WaitGroup
		decryptErrCh = make(chan error, len(records))
		allPsMap     = make(map[string]exo.PermissionRecord, len(records))
		decryptMu    sync.Mutex
	)

	for i, record := range records {
		decryptWg.Add(1)
		go func(i int, record exo.PermissionRecord) {
			defer decryptWg.Done()
			decrypted, err := s.cryptor.DecryptPermission(record)
			if err != nil {
				decryptErrCh <- fmt.Errorf("failed to decrypt permission '%s': %v", record.Id, err)
				return
			}
			records[i] = *decrypted

			// opportunistically build the map during decryption
			decryptMu.Lock()
			allPsMap[decrypted.Slug] = *decrypted
			decryptMu.Unlock()
		}(i, record)
	}

	decryptWg.Wait()
	close(decryptErrCh)

	// handle decryption errors
	if len(decryptErrCh) > 0 {
		var errs []error
		for e := range decryptErrCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to decrypt permission records: %v", errors.Join(errs...))
		}
	}

	// check if cmd permission slugs are real permission's slugs -> if it is real, add to newPsMap,
	// else it will return an error and the operation will not complete.
	// NOTE: map key must be the permission to match the return map of the GetImagePermissions method
	newPsMap := make(map[string]exo.PermissionRecord, len(permissionSlugs))
	for _, slug := range permissionSlugs {
		if p, exists := allPsMap[slug]; exists {
			newPsMap[p.Permission] = p
		} else {
			return fmt.Errorf("permission slug '%s' does not exist", slug)
		}
	}

	// get the current permissions associated with the image
	currentPsMap, _, err := s.GetImagePermissions(imageId)
	if err != nil {

		if strings.Contains(err.Error(), "no permissions found for image") {

			log.Warn(fmt.Sprintf("image '%s' currently has no permissions associated", imageId))
		} else {

			return fmt.Errorf("failed to get current permissions for image '%s': %v", imageId, err)
		}
	}

	// determine which permissions need to be added and which need to be removed
	var (
		toAdd    []exo.PermissionRecord
		toRemove []exo.PermissionRecord
	)

	// build toAdd slice
	// Note: currentPsMap is keyed by permission field, so need to check existence by that key
	for _, p := range newPsMap {
		if _, exists := currentPsMap[p.Permission]; !exists {
			toAdd = append(toAdd, p)
		}
	}

	// build toRemove slice
	// Note: newPsMap is keyed by permission field, so need to check existence by that key
	for _, p := range currentPsMap {
		if _, exists := newPsMap[p.Permission]; !exists {
			toRemove = append(toRemove, p)
		}
	}

	if len(toAdd) > 0 || len(toRemove) > 0 {
		log.Info(fmt.Sprintf("updating permissions for image '%s': %d to add, %d to remove", imageId, len(toAdd), len(toRemove)))
		// perform the additions and removals in parallel
		var (
			xrefWg    sync.WaitGroup
			xrefErrCh = make(chan error, len(toAdd)+len(toRemove))
		)

		// add new permissions associations
		for _, p := range toAdd {
			xrefWg.Add(1)
			go func(permissionId string) {
				defer xrefWg.Done()

				// add the xref record
				xref := ImagePermissionXref{
					Id:           0, // auto-incremented by the database
					ImageId:      imageId,
					PermissionId: permissionId,
					CreatedAt:    data.CustomTime{Time: time.Now().UTC()},
				}

				if err := s.sql.InsertImagePermissionXref(xref); err != nil {
					xrefErrCh <- fmt.Errorf("failed to add permission '%s' to image '%s': %v", p.Slug, imageId, err)
					return
				}

				log.Info(fmt.Sprintf("added permission '%s' to image '%s'", permissionId, imageId))
			}(p.Id)
		}

		// remove old permissions associations
		for _, p := range toRemove {
			xrefWg.Add(1)
			go func(permissionId string) {
				defer xrefWg.Done()

				// remove the xref record
				if err := s.sql.DeleteImagePermissionXref(imageId, permissionId); err != nil {
					xrefErrCh <- fmt.Errorf("failed to remove permission '%s' from image '%s': %v", p.Id, imageId, err)
					return
				}

				log.Info(fmt.Sprintf("removed permission '%s' from image '%s'", permissionId, imageId))
			}(p.Id)
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
				return fmt.Errorf("failed to update image permission xref records: %v", errors.Join(errs...))
			}
		}

	} else {
		log.Info(fmt.Sprintf("no changes to permissions for image '%s'", imageId))
	}

	return nil
}
