package permissions

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
)

type PermissionRecord struct {
	Id          string          `db:"uuid" json:"uuid,omitempty"`
	ServiceName string          `db:"service_name" json:"service_name"`
	Permission  string          `db:"permission" json:"permission"`
	Name        string          `db:"name" json:"name"`
	Description string          `db:"description" json:"description"`
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
