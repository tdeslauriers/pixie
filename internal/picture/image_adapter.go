package picture

import (
	"database/sql"

	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// Repository is an interface for data operations related to pictures/images metadata records.
type Repository interface {

	// ImageExists checks if an image with the given slug index exists in the database.
	ImageExists(slugIndex string) (bool, error)

	// ImageExistsNoPermissions checks if an image with the given slug index exists in the database, but
	// the user does not have permissions to view it.
	ImageExistsNoPermissions(
		slugIndex string,
		userPs map[string]exo.PermissionRecord,
	) (bool, error)

	// ImageExistsArchived checks if an image with the given slug index exists in the database, but is archived.
	ImageExistsArchived(slugIndex string) (bool, error)

	// ImageExistsPublished checks if an image with the given slug index exists in the database, but is unpublished.
	ImageExistsUnpublished(slugIndex string) (bool, error)

	// FindImageByPermissions retrieves an image metadata record by its slug and the user's permissions.  If the user
	// does not have the correct permissions, the record will not return even if it does exist.
	FindImageByPermissions(
		slugIndex string,
		userPs map[string]exo.PermissionRecord,
	) (*api.ImageRecord, error)

	// InsertImage inserts a new image metadata record into the database.
	// Note: fields must be encrypted prior to calling this function.
	InsertImage(record api.ImageRecord) error

	// UpdateImage updates an existing image metadata record in the database.
	// Note: fields must be encrypted prior to calling this function.
	UpdateImage(record api.ImageRecord) error
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

// ImageExists checks if an image with the given slug index exists in the database.
func (r *repository) ImageExists(slugIndex string) (bool, error) {

	qry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
			WHERE i.slug_index = ?
		)`

	return data.SelectExists(r.sql, qry, slugIndex)
}

// ImageExistsNoPermissions checks if an image with the given slug index exists in the database, but
// the user does not have permissions to view it.
func (r *repository) ImageExistsNoPermissions(
	slugIndex string,
	userPs map[string]exo.PermissionRecord,
) (bool, error) {

	qry := BuildImagePermissionsQry(userPs)

	// create the []args ...interface{} slice
	args := make([]interface{}, 0, len(userPs)+1)

	// add the slug index as the first argument
	args = append(args, slugIndex)

	// if the user is not a curator/admin, add the permission uuids as the remaining arguments
	if _, ok := userPs[util.PermissionCurator]; !ok {
		for _, p := range userPs {
			args = append(args, p.Id)
		}
	}

	return data.SelectExists(r.sql, qry, args...)
}

// ImageExistsArchived checks if an image with the given slug index exists in the database, but is archived.
func (r *repository) ImageExistsArchived(slugIndex string) (bool, error) {

	qry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
			WHERE i.slug_index = ?
				AND i.is_archived = TRUE
		)`

	return data.SelectExists(r.sql, qry, slugIndex)
}

// ImageExistsPublished checks if an image with the given slug index exists in the database, but is unpublished.
func (r *repository) ImageExistsUnpublished(slugIndex string) (bool, error) {

	qry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
			WHERE i.slug_index = ?
				AND i.is_published = FALSE
		)`

	return data.SelectExists(r.sql, qry, slugIndex)
}

// FindImageByPermissions retrieves an image metadata record by its slug index and the user's permissions.  If the user
// does not have the correct permissions, the record will not return even if it does exist.
func (r *repository) FindImageByPermissions(
	slugIndex string,
	userPs map[string]exo.PermissionRecord,
) (*api.ImageRecord, error) {

	// build query based on the user's permissions
	qry := BuildGetImageQuery(userPs)

	// create the []args ...interface{} slice
	args := make([]interface{}, 0, len(userPs)+1)

	// add the slug index as the first argument
	args = append(args, slugIndex)

	// if the user is not a curator/admin, add the permission uuids as the remaining arguments
	if _, ok := userPs[util.PermissionCurator]; !ok {
		for _, p := range userPs {
			args = append(args, p.Id)
		}
	}

	record, err := data.SelectOneRecord[api.ImageRecord](r.sql, qry, args...)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

// InsertImage inserts a new image metadata record into the database.
// Note: fields must be encrypted prior to calling this function.
func (r *repository) InsertImage(record api.ImageRecord) error {

	qry := `
		INSERT INTO image (
			uuid, 
			title,
			description,
			file_name,
			file_type,
			object_key,
			slug,
			slug_index,
			width,
			height,
			size,
			image_date,
			created_at,
			updated_at,
			is_archived,
			is_published
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return data.InsertRecord(r.sql, qry, record)
}

// UpdateImage updates an existing image metadata record in the database.
// Note: fields must be encrypted prior to calling this function.
func (r *repository) UpdateImage(record api.ImageRecord) error {

	qry := `
		UPDATE image SET
			title = ?,
			description = ?,
			object_key = ?,
			image_date = ?,
			updated_at = ?,
			is_archived = ?,
			is_published = ?
		WHERE slug_index = ?`

	return data.UpdateRecord(
		r.sql,
		qry,
		record.Title,       // update
		record.Description, // update
		record.ObjectKey,   // update
		record.ImageDate,   // update
		record.UpdatedAt,   // update
		record.IsArchived,  // update
		record.IsPublished, // update
		record.SlugIndex,   // where clause
	)
}
