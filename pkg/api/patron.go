package api

import (
	"fmt"

	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
)

// PatronRecord is a model which represents a patron record in the database.
type Patron struct {
	Id          string                 `json:"uuid,omitempty"`
	Username    string                 `json:"username"`
	Slug        string                 `json:"slug,omitempty"`
	CreatedAt   data.CustomTime        `json:"created_at,omitempty"`
	UpdatedAt   data.CustomTime        `json:"updated_at,omitempty"`
	IsArchived  bool                   `json:"is_archived"`
	IsActive    bool                   `json:"is_active"`
	Permissions []exo.PermissionRecord `json:"permissions,omitempty"`
}

// PatronRegisterCmd is a command for registering a new patron.
type PatronRegisterCmd struct {
	Username string `json:"username"`
}

// Validate validates the PatronRegisterCmd -> input validation.
func (c *PatronRegisterCmd) Validate() error {
	if c.Username == "" {
		return fmt.Errorf("username is required")
	}

	if err := validate.IsValidEmail(c.Username); err != nil {
		return fmt.Errorf("username %s is not a valid email address", c.Username)
	}

	return nil
}
