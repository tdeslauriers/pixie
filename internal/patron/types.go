package patron

import (
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
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
