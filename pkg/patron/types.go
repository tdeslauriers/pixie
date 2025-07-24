package patron

import (
	"fmt"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
)

// Handler is an interface aggregation of all handler interfaces that manage patron records.
type Handler interface {
	PermissionHandler
	PatronRegisterHandler
}

var _ Handler = (*handler)(nil)

// NewHandler creates a new Handler instance, returning a pointer to the concrete implementation.
func NewHandler(s Service, s2s, iam jwt.Verifier) Handler {
	return &handler{
		PermissionHandler:     NewPermissionHandler(s, s2s, iam),
		PatronRegisterHandler: NewPatronRegisterHandler(s, s2s),
	}
}

// handler is the concrete implementation of the Handler interface.
type handler struct {
	PermissionHandler
	PatronRegisterHandler
}

// Service is an interface aggregation of all service interfaces that manage patron records.
type Service interface {
	PatronService
}

// NewService creates a new Service instance, returning a pointer to the concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &service{
		PatronService: NewPatronService(sql, i, c),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
type service struct {
	PatronService
}

// PatronRecord is a model which represents a patron record in the database.
type PatronRecord struct {
	Id         string          `db:"uuid" json:"uuid,omitempty"`
	Username   string          `db:"username" json:"username"` // encrypted
	UserIndex  string          `db:"user_index" json:"user_index,omitempty"`
	Slug       string          `db:"slug" json:"slug,omitempty"` // encrypted
	SlugIndex  string          `db:"slug_index" json:"slug_index,omitempty"`
	CreatedAt  data.CustomTime `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt  data.CustomTime `db:"updated_at" json:"updated_at,omitempty"`
	IsArchived bool            `db:"is_archived" json:"is_archived"`
	IsActive   bool            `db:"is_active" json:"is_active"`
}

// PatronPermissionRecord is a model which represents a patron permission xref record in the database.
type PatronPermissionRecord struct {
	Id           int             `db:"uuid" json:"uuid,omitempty"`
	PatronId     string          `db:"patron_uuid" json:"patron_id,omitempty"`
	PermissionId string          `db:"permission_uuid" json:"permission_id,omitempty"`
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at,omitempty"`
}

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
