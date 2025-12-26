package pipeline

import (
	"database/sql"
	"errors"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// Repository is an interface for data operations related to image processing in the image processing pipeline.
type Repository interface {

	// FindAllAlbums retrieves all album records from the database.
	FindAllAlbums() ([]api.AlbumRecord, error)

	// FindImageBySlugIndex retrieves an image record by its slug index.
	FindImage(slugIndex string) (*api.ImageRecord, error)

	// FindImageAlbums retrieves all albums associated with a given image's uuid.
	FindImageAlbums(imageId string) ([]api.AlbumRecord, error)

	// InsertAlbum inserts a new album metadata record into the database.
	InsertAlbum(record api.AlbumRecord) error

	// InsertAlbumImageXref inserts a xref record into the ablum_image table in the database.
	InsertAlbumImageXref(xref api.AlbumImageXref) error

	// UpdateImage updates an existing image metadata record in the database.
	// Note: fields must be encrypted prior to calling this function.
	UpdateImage(record api.ImageRecord) error
}

// NewRepository creates a new Repository instance, returning a pointer to the concrete implementation.
func NewRepository(db *sql.DB) Repository {
	return &repository{
		sql: db,
	}
}

var _ Repository = (*repository)(nil)

// repository is the concrete implementation of the Repository interface.
type repository struct {
	sql *sql.DB
}

// FindAllAlbums retrieves all album records from the database.
func (r *repository) FindAllAlbums() ([]api.AlbumRecord, error) {

	qry := `
		SELECT 
			uuid,
			title,
			description,
			slug,
			slug_index,
			created_at,
			updated_at,
			is_archived
		FROM album`

	return data.SelectRecords[api.AlbumRecord](r.sql, qry)
}

// FindImage retrieves an image metadata record by its slug index.
func (r *repository) FindImage(slugIndex string) (*api.ImageRecord, error) {

	qry := `
		SELECT 
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
		FROM image 
		WHERE slug_index = ?`

	image, err := data.SelectOneRecord[api.ImageRecord](r.sql, qry, slugIndex)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("no image record found") // slug via slug index
		}
		return nil, err
	}

	return &image, nil
}

// FindImageAlbums retrieves all albums associated with a given image's uuid.
func (r *repository) FindImageAlbums(imageId string) ([]api.AlbumRecord, error) {

	qry := `
		SELECT 
			a.uuid,
			a.title,
			a.description,
			a.slug,
			a.slug_index,
			a.created_at,
			a.updated_at,
			a.is_archived
		FROM album a
			LEFT OUTER JOIN album_image ai ON a.uuid = ai.album_uuid
		WHERE ai.image_uuid = ?`

	return data.SelectRecords[api.AlbumRecord](r.sql, qry, imageId)
}

// InsertAlbum inserts a new album metadata record into the database.
func (r *repository) InsertAlbum(record api.AlbumRecord) error {

	qry := `
		INSERT INTO album (
			uuid,
			title,
			description,
			slug,
			slug_index,
			created_at,
			updated_at,
			is_archived
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	return data.InsertRecord(r.sql, qry, record)
}

// InsertAlbumImageXref inserts a xref record into the ablum_image table in the database.
func (r *repository) InsertAlbumImageXref(xref api.AlbumImageXref) error {

	qry := `
		INSERT INTO album_image (
			id,
			album_uuid,
			image_uuid,
			created_at
		) VALUES (?, ?, ?, ?)`

	return data.InsertRecord(r.sql, qry, xref)
}

// UpdateImage updates an existing image metadata record in the database.
// Note: fields must be encrypted prior to calling this function.
func (r *repository) UpdateImage(record api.ImageRecord) error {

	qry := `
		UPDATE image SET 
			object_key = ?,
			width = ?,
			height = ?,
			image_date = ?,
			updated_at = ?,
			is_published = ?
		WHERE uuid = ?`

	return data.UpdateRecord(
		r.sql,
		qry,
		record.ObjectKey,   // to update
		record.Width,       // to update
		record.Height,      // to update
		record.ImageDate,   // to update
		record.UpdatedAt,   // to update
		record.IsPublished, // to update
		record.Id,          // where clause
	)
}
