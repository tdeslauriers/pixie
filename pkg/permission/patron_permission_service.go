package permission

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
)

// PermissionsService is the interface for managing a patrons permissions in the database.
type PatronPermissionService interface {
	// GetPatronPermissions returns a map and a list of a patron's permissions.
	// Key is the permission field, value is the permission record.
	GetPatronPermissions(username string) (map[string]exo.PermissionRecord, []exo.PermissionRecord, error)

	// AddPermissionToPatron adds a permission to a patron's permissions in the database.
	AddPermissionToPatron(patronId, permissionId string) error

	// RemovePermissionFromPatron removes a permission from a patron's permissions in the database.
	RemovePermissionFromPatron(patronId, permissionId string) error
}

// NewPatronPermissionService creates a new PatronPermissionService instance, returning a pointer to the concrete implementation.
func NewPatronPermissionService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) PatronPermissionService {
	return &patronPermissionService{
		sql:     sql,
		indexer: i,
		cryptor: exo.NewPermissionCryptor(c),

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
	cryptor exo.PermissionCryptor

	logger *slog.Logger
}

// GetPatronPermissions retrieves a patron's permissions from the database.
// Key is the permission field, value is the permission record.
func (s *patronPermissionService) GetPatronPermissions(username string) (map[string]exo.PermissionRecord, []exo.PermissionRecord, error) {

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
	var records []exo.PermissionRecord
	if err := s.sql.SelectRecords(qry, &records, index); err != nil {
		s.logger.Error(fmt.Sprintf("failed to retrieve permissions for patron '%s': %v", username, err))
		return nil, nil, err
	}

	// It is possible for patrons to have zero permissions.
	// This will be the default case, so we return an empty map and slice.
	if len(records) == 0 {
		s.logger.Warn(fmt.Sprintf("no permissions found for patron '%s'", username))
	}

	// decrypt and create a map of permissions
	psMap := make(map[string]exo.PermissionRecord, len(records))
	for i, record := range records {
		decrypted, err := s.cryptor.DecryptPermission(record)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decrypt permission '%s': %v", record.Id, err)
		}
		records[i] = *decrypted
		psMap[decrypted.Permission] = *decrypted
	}

	// return the permissions
	return psMap, records, nil
}

// AddPermissionToPatron is the concrete implementation of the interface method which
// adds a permission to a patron's permissions in the database.
func (s *patronPermissionService) AddPermissionToPatron(patronId, permissionId string) error {

	// validate patronId is a well-formed UUID
	if !validate.IsValidUuid(patronId) {
		return fmt.Errorf("invalid patron ID: %s", patronId)
	}

	// validate permissionId is a well-formed UUID
	if !validate.IsValidUuid(permissionId) {
		return fmt.Errorf("invalid permission ID: %s", permissionId)
	}

	// build the xref record to insert
	xref := PatronPermissionXrefRecord{
		Id:           0, // auto-incremented by the database
		PatronId:     patronId,
		PermissionId: permissionId,
		CreatedAt:    data.CustomTime{Time: time.Now().UTC()},
	}

	// insert the xref record into the database
	qry := `INSERT INTO patron_permission (id, patron_uuid, permission_uuid, created_at) VALUES (?, ?, ?, ?)`
	if err := s.sql.InsertRecord(qry, xref); err != nil {
		return fmt.Errorf("failed to add permission '%s' to patron '%s': %v", permissionId, patronId, err)
	}

	return nil
}

// RemovePermissionFromPatron is the concrete implementation of the interface method which
// removes a permission from a patron's permissions in the database.
func (s *patronPermissionService) RemovePermissionFromPatron(patronId, permissionId string) error {

	// validate patronId is a well-formed UUID
	if !validate.IsValidUuid(patronId) {
		return fmt.Errorf("invalid patron ID: %s", patronId)
	}

	// validate permissionId is a well-formed UUID
	if !validate.IsValidUuid(permissionId) {
		return fmt.Errorf("invalid permission ID: %s", permissionId)
	}

	// delete the xref record from the database
	qry := `DELETE FROM patron_permission WHERE patron_uuid = ? AND permission_uuid = ?`
	if err := s.sql.DeleteRecord(qry, patronId, permissionId); err != nil {
		return fmt.Errorf("failed to remove permission '%s' from patron '%s': %v", permissionId, patronId, err)
	}

	return nil
}
