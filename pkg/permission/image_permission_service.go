package permission

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

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
}

// NewImagePermissionService creates a new ImagePermissionService instance, returning a pointer to the concrete implementation.
func NewImagePermissionService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) ImagePermissionService {
	return &imagePermissionService{
		sql:     sql,
		indexer: i,
		cryptor: exo.NewPermissionCryptor(c),

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePermissions)).
			With(slog.String(util.ComponentKey, util.ComponentImagePermissions)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ ImagePermissionService = (*imagePermissionService)(nil)

// imagePermissionService is the concrete implementation of the ImagePermissionService interface.
type imagePermissionService struct {
	sql     data.SqlRepository
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
	qry := `
		SELECT 
			p.uuid,
			p.service_name,
			p.permission,
			p.name,
			p.description,
			p.created_at,
			p.active,
			p.slug,
			p.slug_index
		FROM permission p
			LEFT OUTER JOIN image_permission ip ON p.uuid = ip.permission_uuid
		WHERE ip.image_uuid = ?
			AND p.active = TRUE`
	var records []exo.PermissionRecord
	if err := s.sql.SelectRecords(qry, &records, imageId); err != nil {
		s.logger.Error(fmt.Sprintf("failed to retrieve permissions for image '%s': %v", imageId, err))
		return nil, nil, err
	}

	if len(records) == 0 {
		noneMsg := fmt.Sprintf("no permissions found for image '%s'", imageId)
		s.logger.Info(noneMsg)
		return nil, nil, fmt.Errorf(noneMsg)
	}

	// decrypt and create a map of permissions
	var (
		wg    sync.WaitGroup
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
			psMap[decrypted.Permission] = *decrypted
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
