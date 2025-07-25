package permission

import (
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
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

// PatronPermissionXrefRecord is a model which represents a patron permission cross-reference record in the database.
type PatronPermissionXrefRecord struct {
	Id           int             `db:"id" json:"id,omitempty"`
	PatronId     string          `db:"patron_uuid" json:"patron_uuid,omitempty"`
	PermissionId string          `db:"permission_uuid" json:"permission_uuid,omitempty"`
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at,omitempty"`
}
