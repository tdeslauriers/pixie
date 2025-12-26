package permission

import (
	"database/sql"

	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
)

// Repository is an interface for data operations related to permissions.
type Repository interface {

	// FindAllPermissions retrieves all permission records from the database.
	FindAllPermissions() ([]exo.PermissionRecord, error)

	// FindPatronPermissions retrieves the active permission records associated with a patron by their user_index.
	FindPatronPermissions(userIndex string) ([]exo.PermissionRecord, error)

	// FindImagePermissions retrieves the active permission records associated with an image by its UUID.
	FindImagePermissions(imageId string) ([]exo.PermissionRecord, error)

	// InsertPatronPermissionXref inserts a patron permission cross-reference record into the database.
	InsertPatronPermissionXref(xref PatronPermissionXrefRecord) error

	// InsertImagePermissionXref inserts an image permission cross-reference record into the database.
	InsertImagePermissionXref(xref ImagePermissionXref) error

	// DeletePatronPermissionXref deletes a patron permission cross-reference record from the database.
	DeletePatronPermissionXref(patronId, permissionId string) error

	// DeleteImagePermissionXref deletes an image permission cross-reference record from the database.
	DeleteImagePermissionXref(imageId, permissionId string) error
}

// NewRepository creates a new Repository instance, returning a pointer to the concrete implementation.
func NewRepository(sql *sql.DB) Repository {

	return &repository{
		sql: sql,
	}
}

var _ Repository = (*repository)(nil)

// repository is the concrete implementation of the Repository interface.
type repository struct {
	sql *sql.DB
}

// FindAllPermissions retrieves all permission records from the database.
func (r *repository) FindAllPermissions() ([]exo.PermissionRecord, error) {

	qry := `
		SELECT 
			uuid,
			service_name,
			permission,
			name,
			description,
			created_at,
			active,
			slug,
			slug_index
		FROM permission`

	return data.SelectRecords[exo.PermissionRecord](r.sql, qry)
}

// FindPatronPermissions retrieves the active permission records associated with a patron by their user_index.
func (r *repository) FindPatronPermissions(userIndex string) ([]exo.PermissionRecord, error) {

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

	return data.SelectRecords[exo.PermissionRecord](r.sql, qry, userIndex)
}

// FindImagePermissions retrieves the active permission records associated with an image by its UUID.
func (r *repository) FindImagePermissions(imageId string) ([]exo.PermissionRecord, error) {

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
			LEFT OUTER JOIN image_permission ip ON p.uuid = ip.permission_uuid
		WHERE ip.image_uuid = ?
			AND p.active = TRUE`

	return data.SelectRecords[exo.PermissionRecord](r.sql, qry, imageId)
}

// InsertPatronPermissionXref inserts a patron permission cross-reference record into the database.
func (r *repository) InsertPatronPermissionXref(xref PatronPermissionXrefRecord) error {

	qry := `
		INSERT INTO patron_permission (
			id,
			patron_uuid,
			permission_uuid,
			created_at
		) VALUES (?, ?, ?, ?)`

	return data.InsertRecord(r.sql, qry, xref)
}

// InsertImagePermissionXref inserts an image permission cross-reference record into the database.
func (r *repository) InsertImagePermissionXref(xref ImagePermissionXref) error {

	qry := `
		INSERT INTO image_permission (
			id, 
			image_uuid, 
			permission_uuid, 
			created_at) 
		VALUES (?, ?, ?, ?)`

	return data.InsertRecord(r.sql, qry, xref)
}

// DeletePatronPermissionXref deletes a patron permission cross-reference record from the database.
func (r *repository) DeletePatronPermissionXref(patronId, permissionId string) error {

	qry := `
		DELETE FROM patron_permission 
		WHERE patron_uuid = ? AND permission_uuid = ?`

	return data.DeleteRecord(r.sql, qry, patronId, permissionId)
}

// DeleteImagePermissionXref deletes an image permission cross-reference record from the database.
func (r *repository) DeleteImagePermissionXref(imageId, permissionId string) error {

	qry := `
		DELETE FROM image_permission 
		WHERE image_uuid = ? AND permission_uuid = ?`

	return data.DeleteRecord(r.sql, qry, imageId, permissionId)
}
