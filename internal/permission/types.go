package permission

import (
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
)

// PermissionsService is a service aggregation interface for all services managing permissions.
type Service interface {
	permissions.Service
	PatronPermissionService
	ImagePermissionService
}

// NewService creates a new service instance, returning a pointer to the concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) Service {
	return &service{
		Service:                 permissions.NewService(sql, i, c),
		PatronPermissionService: NewPatronPermissionService(sql, i, c),
		ImagePermissionService:  NewImagePermissionService(sql, i, c),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
// It aggregates all the service interfaces that manage permissions.
type service struct {
	permissions.Service
	PatronPermissionService
	ImagePermissionService
}

// PatronPermissionXrefRecord is a model which represents a patron permission cross-reference record in the database.
type PatronPermissionXrefRecord struct {
	Id           int             `db:"id" json:"id,omitempty"`
	PatronId     string          `db:"patron_uuid" json:"patron_uuid,omitempty"`
	PermissionId string          `db:"permission_uuid" json:"permission_uuid,omitempty"`
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at,omitempty"`
}

// MapPermissionRecordsToApi is a helper method which maps a slice of
// PermissionRecords to a slice of API Permission objects.
func MapPermissionRecordsToApi(records []permissions.PermissionRecord) ([]permissions.Permission, error) {
	if len(records) == 0 {
		return nil, nil
	}

	permissionsList := make([]permissions.Permission, len(records))
	for i, record := range records {
		permissionsList[i] = permissions.Permission{
			Id:          record.Id,
			ServiceName: record.ServiceName,
			Permission:  record.Permission,
			Name:        record.Name,
			Description: record.Description,
			CreatedAt:   record.CreatedAt,
			Active:      record.Active,
			Slug:        record.Slug,
		}
	}
	return permissionsList, nil
}

// ImagePermissionXref is a model which represents an image_permission xref record in the database.
type ImagePermissionXref struct {
	Id           int             `db:"id" json:"id"`                         // Unique identifier for the xref record
	ImageId      string          `db:"image_uuid" json:"image_id"`           // UUID of the image
	PermissionId string          `db:"permission_uuid" json:"permission_id"` // UUID of the permission
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at"`         // Timestamp when the xref was created
}
