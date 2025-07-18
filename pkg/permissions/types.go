package permissions

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
)

// PermissionsService is a service aggregation interface for all services managing permissions.
type Service interface {
	PermissionsService
	PatronPermissionService
}

// NewService creates a new service instance, returning a pointer to the concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &service{
		PermissionsService:      NewPermissionsService(sql, i, c),
		PatronPermissionService: NewPatronPermissionService(sql, i, c),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
// It aggregates all the service interfaces that manage permissions.
type service struct {
	PermissionsService
	PatronPermissionService
}

// PermissionRecord is a model which represents a permission record in the database.
type PermissionRecord struct {
	Id          string          `db:"uuid" json:"uuid,omitempty"` 
	ServiceName string          `db:"service_name" json:"service_name"` 
	Permission  string          `db:"permission" json:"permission"` // encrypted
	Name        string          `db:"name" json:"name"` // encrypted
	Description string          `db:"description" json:"description"` // encrypted
	CreatedAt   data.CustomTime `db:"created_at" json:"created_at,omitempty"`
	Active      bool            `db:"active" json:"active"`
	Slug        string          `db:"slug" json:"slug,omitempty"`
	SlugIndex   string          `db:"slug_index" json:"slug_index,omitempty"`
}

// Validate checks if the permission is valid/well-formed
func (p *PermissionRecord) Validate() error {

	// validate id if it is set
	if p.Id != "" {
		if !validate.IsValidUuid(strings.TrimSpace(p.Id)) {
			return fmt.Errorf("invalid permission id in permission payload")
		}
	}

	// check service name
	if ok, err := validate.IsValidServiceName(strings.TrimSpace(p.ServiceName)); !ok {
		return fmt.Errorf("invalid service name in permission payload: %v", err)
	}

	// check permission name
	if ok, err := validate.IsValidPermissionName(strings.TrimSpace(p.Name)); !ok {
		return fmt.Errorf("invalid permission name in permission payload: %v", err)
	}

	// check description length
	if validate.TooShort(p.Description, 2) || validate.TooLong(p.Description, 256) {
		return fmt.Errorf("invalid description in permission payload")
	}

	// check slug if it is set
	if p.Slug != "" {
		if !validate.IsValidUuid(strings.TrimSpace(p.Slug)) {
			return fmt.Errorf("invalid slug in permission payload")
		}
	}

	return nil
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
