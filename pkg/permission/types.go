package permission

import (
	"fmt"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
)

// PermissionsService is a service aggregation interface for all services managing permissions.
type Service interface {
	permissions.Service
	PatronPermissionService
}

// NewService creates a new service instance, returning a pointer to the concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &service{
		Service:                 permissions.NewService(sql, i, c),
		PatronPermissionService: NewPatronPermissionService(sql, i, c),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
// It aggregates all the service interfaces that manage permissions.
type service struct {
	permissions.Service
	PatronPermissionService
}

// UpdatePermissionsCmd us a model used as a command to update the permissions associated with an entity.
// Note, the entity could be a user, an image, an album, or any other resourse identifier,
// eg, email address, image slug, album slug, etc.
type UpdatePermissionsCmd struct {
	Entity      string   `json:"entity"`
	Permissions []string `json:"permissions"`
}

// Validate checks if the update permissions command is valid/well-formed
func (cmd *UpdatePermissionsCmd) Validate() error {

	// light-weight validation of the entity since it is a lookup and can be many
	// things like a user email, image slug, album slug, etc.
	if len(cmd.Entity) < 2 || len(cmd.Entity) > 64 {
		return fmt.Errorf("invalid entity in update permissions command: must be between 2 and 64 characters")
	}

	// check permission slugs
	// note: for now, these are uuids, but could be any string in the future
	for _, permission := range cmd.Permissions {
		if !validate.IsValidUuid(permission) {
			return fmt.Errorf("invalid permission slug in update permissions command: %s", permission)
		}
	}

	return nil
}

// PatronPermissionXrefRecord is a model which represents a patron permission cross-reference record in the database.
type PatronPermissionXrefRecord struct {
	Id           int             `db:"id" json:"id,omitempty"`
	PatronId     string          `db:"patron_uuid" json:"patron_uuid,omitempty"`
	PermissionId string          `db:"permission_uuid" json:"permission_uuid,omitempty"`
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at,omitempty"`
}
