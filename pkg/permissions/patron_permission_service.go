package permissions

import (
	"fmt"
	"log/slog"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
)

// PermissionsService is the interface for managing a patrons permissions in the database.
type PatronPermissionService interface {
	// GetPatronPermissions returns a map and a list of a patron's permissions.
	GetPatronPermissions(username string) (map[string]PermissionRecord, []PermissionRecord, error)
}

// NewPatronPermissionService creates a new PatronPermissionService instance, returning a pointer to the concrete implementation.
func NewPatronPermissionService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) PatronPermissionService {
	return &patronPermissionService{
		sql:     sql,
		indexer: i,
		cryptor: NewPermissionCryptor(c),
		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePermissions)).
			With(slog.String(util.ComponentKey, util.ComponentPermissions)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ PatronPermissionService = (*patronPermissionService)(nil)

type patronPermissionService struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor PermissionCryptor

	logger *slog.Logger
}

// GetPatronPermissions retrieves a patron's permissions from the database.
func (s *patronPermissionService) GetPatronPermissions(username string) (map[string]PermissionRecord, []PermissionRecord, error) {

	// validate the username
	// redundant check, but good practice
	if err := validate.IsValidEmail(username); err != nil {
		return nil, nil, err
	}

	// get the user's blind index
	index, err := s.indexer.ObtainBlindIndex(username)
	if err != nil {
		return nil, nil, err
	}

	// query the database for the patron's permissions
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
			LEFT OUTER JOIN patron_permission pp ON p.uuid = pp.permission_uuid
			LEFT OUTER JOIN patron pat ON pp.patron_uuid = pat.uuid
		WHERE pat.user_index = ?
			AND p.active = TRUE`
	var records []PermissionRecord
	if err := s.sql.SelectRecords(qry, &records, index); err != nil {
		s.logger.Error(fmt.Sprintf("failed to retrieve permissions for patron '%s': %v", username, err))
		return nil, nil, err
	}

	if len(records) == 0 {
		s.logger.Info(fmt.Sprintf("no permissions found for patron '%s'", username))
		return nil, nil, fmt.Errorf("no permissions found for patron '%s'", username)
	}

	// decrypt and create a map of permissions
	psMap := make(map[string]PermissionRecord, len(records))
	for i, record := range records {
		decrypted, err := s.cryptor.DecryptPermission(record)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decrypt permission '%s': %v", record.Id, err)
		}
		records[i] = *decrypted
		psMap[decrypted.Name] = *decrypted
	}

	// return the permissions
	return psMap, records, nil
}
