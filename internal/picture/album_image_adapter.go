package picture

import (
	"database/sql"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// AlbumImageRepository is an interface for data operations related to album-image data relationships, xrefs, and joins.
type AlbumImageRepository interface {

	// FindAllAlbums retrieves all album records from the database.
	FindAllAlbums() ([]api.AlbumRecord, error)

	// FindAlbumByImgId retrieves the album associated with a given image's uuid.
	FindAlbumByImgId(imageId string) ([]api.AlbumRecord, error)

	// InsertImagePermissionXref inserts an image permission cross-reference record into the database.
	InsertImagePermissionXref(xref ImagePermissionXref) error

	// InsertAlbumImageXref inserts a xref record into the ablum_image table in the database.
	InsertAlbumImageXref(xref api.AlbumImageXref) error

	// DeleteAlbumImageXref deletes an album-image xref record from the database.
	DeleteAlbumImageXref(albumId, imageId string) error
}

// NewAlbumImageRepository creates a new AlbumImageRepository instance, returning a pointer to the concrete implementation.
func NewAlbumImageRepository(db *sql.DB) AlbumImageRepository {
	return &albumImageRepository{
		sql: db,
	}
}

var _ AlbumImageRepository = (*albumImageRepository)(nil)

// albumImageRepository is the concrete implementation of the AlbumImageRepository interface.
type albumImageRepository struct {
	sql *sql.DB
}

// FindAllAlbums retrieves all album records from the database.
func (r *albumImageRepository) FindAllAlbums() ([]api.AlbumRecord, error) {

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

// FindAlbumByImgId retrieves the album associated with a given image's uuid.
func (r *albumImageRepository) FindAlbumByImgId(imageId string) ([]api.AlbumRecord, error) {

	// build the query to get the albums associated with the image
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

// InsertImagePermissionXref inserts an image permission cross-reference record into the database.
func (r *albumImageRepository) InsertImagePermissionXref(xref ImagePermissionXref) error {

	qry := `
		INSERT INTO image_permission (
			id, 
			image_uuid, 
			permission_uuid, 
			created_at) 
		VALUES (?, ?, ?, ?)`

	return data.InsertRecord(r.sql, qry, xref)
}

// InsertAlbumImageXref inserts a xref record into the ablum_image table in the database.
func (a *albumImageRepository) InsertAlbumImageXref(xref api.AlbumImageXref) error {

	qry := `
		INSERT INTO album_image (
			id,
			album_uuid,
			image_uuid,
			created_at
		) VALUES (?, ?, ?, ?)`

	return data.InsertRecord(a.sql, qry, xref)
}

// DeleteAlbumImageXref deletes an album-image xref record from the database.
func (a *albumImageRepository) DeleteAlbumImageXref(albumId, imageId string) error {

	qry := `
		DELETE FROM album_image 
		WHERE album_uuid = ? AND image_uuid = ?`

	return data.DeleteRecord(a.sql, qry, albumId, imageId)
}
