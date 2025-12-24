package album

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/tdeslauriers/carapace/pkg/data"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// AlbumRepository defines the interface for album data storage operations.
type AlbumRepository interface {

	// FindAllowedAlbums takes a map of the user's permissions and builds a query to find all albums
	// that the user has access to based on those permissions in the database.
	FindAllowedAlbums(userPermissions map[string]exo.PermissionRecord) ([]api.AlbumRecord, error)

	// FindAllowedAlbumsData retrieves all album-image records (imnage meta data not actual images) a user
	// has permission to view.
	FindAllowedAlbumsData(psMap map[string]exo.PermissionRecord) ([]api.AlbumImageRecord, error)

	// FindAlbumImagesData retrieves all image meta data for images in the specified album by slug index.
	// Note: it only returns meta data the user has access to see via permissions
	FindAlbumImagesData(
		albumSlugIndex string,
		psMap map[string]exo.PermissionRecord,
	) ([]api.AlbumImageRecord, error)

	// AlbumExists checks if an album exists by its slug index.
	AlbumExists(slugIndex string) (bool, error)

	// AlbumIsArchived checks if an album exists but is archived by its slug index.
	AlbumIsArchived(slugIndex string) (bool, error)

	// InsertAlbum inserts an album record into the database.
	InsertAlbum(album api.AlbumRecord) error

	// InsertAlbumImageXref inserts a xref record into the ablum_image table in the database.
	InsertAlbumImageXref(xref api.AlbumImageXref) error

	// UpdateAlbum updates an existing album record in the database.
	// Note: not all fields are allowed to be updated.  Also, relevant fields need to be encrypted
	// before calling this method.
	UpdateAlbum(album api.AlbumRecord) error
}

// NewAlbumRepository creates a new instance of AlbumRepository interface, returning
// a pointer to the concrete implementation.
func NewAlbumRepository(db *sql.DB) AlbumRepository {

	// implementation details
	return &albumAdapter{
		db: db,
	}
}

var _ AlbumRepository = (*albumAdapter)(nil) // compile-time interface check

// albumAdapter is a concrete implementation of AlbumRepository.
type albumAdapter struct {
	db *sql.DB
}

// FindAllowedAlbums takes a map of the user's permissions and builds a query to find all albums
// that the user has access to based on those permissions in the database.
func (a *albumAdapter) FindAllowedAlbums(userPermissions map[string]exo.PermissionRecord) ([]api.AlbumRecord, error) {

	// build album query based on permissions
	qry, err := BuildAlbumsQuery(userPermissions)
	if err != nil {
		return nil, fmt.Errorf("failed to build album permission query: %v", err)
	}

	// convert the permissions map into a variatic slice of interface{}, ie args ...interface{}
	args := make([]interface{}, 0, len(userPermissions))
	// if user is curator, no need to filter by permissions
	if _, ok := userPermissions["CURATOR"]; !ok {
		for _, p := range userPermissions {
			args = append(args, p.Id)
		}
	}

	// execute query
	return data.SelectRecords[api.AlbumRecord](a.db, qry, args...)
}

// FindAllowedAlbumsData retrieves all album-image records (imnage meta data not actual images) a user
// has permission to view.
func (a *albumAdapter) FindAllowedAlbumsData(psMap map[string]exo.PermissionRecord) ([]api.AlbumImageRecord, error) {

	// build album query based on permissions
	qry, err := BuildAllAlbumsImagesQuery(psMap)
	if err != nil {
		return nil, fmt.Errorf("failed to build album permission query: %v", err)
	}

	// convert the permissions map into a variatic slice of interface{}, ie args ...interface{}
	args := make([]interface{}, 0, len(psMap))
	// if user is curator, no need to filter by permissions
	if _, ok := psMap["CURATOR"]; !ok {
		for _, p := range psMap {
			args = append(args, p.Id)
		}
	}

	// execute query
	return data.SelectRecords[api.AlbumImageRecord](a.db, qry, args...)
}

// FindAlbumImagesData retrieves all image meta data for images in the specified album by slug index.
// Note: it only returns meta data the user has access to see via permissions
func (a *albumAdapter) FindAlbumImagesData(
	albumSlugIndex string,
	psMap map[string]exo.PermissionRecord,
) ([]api.AlbumImageRecord, error) {

	// build the album query with the users permissions
	qry, err := BuildAlbumImagesQuery(psMap)
	if err != nil {
		return nil, errors.New("failed to create query for album slug")
	}

	// convert the permissions map into a variatic slice of interface{}, ie args ...interface{}
	args := make([]interface{}, 0, len(psMap)+1) // capacity needs to include the slug index
	args = append(args, albumSlugIndex)          // index in first args position
	// if user is curator, no need to filter by permissions
	if _, ok := psMap["CURATOR"]; !ok {
		// append the permissions uuids to the args slice
		for _, p := range psMap {
			args = append(args, p.Id)
		}
	}

	// execute query
	return data.SelectRecords[api.AlbumImageRecord](a.db, qry, args...)
}

// AlbumExists checks if an album exists by its slug index.
func (a *albumAdapter) AlbumExists(slugIndex string) (bool, error) {

	qry := `
		SELECT EXISTS (
			SELECT 1
			FROM album
			WHERE slug_index = ?
		)`

	return data.SelectExists(a.db, qry, slugIndex)
}

// AlbumIsArchived checks if an album exists but is archived by its slug index.
func (a *albumAdapter) AlbumIsArchived(slugIndex string) (bool, error) {

	qry := `
		SELECT EXISTS (
			SELECT 1
			FROM album
			WHERE slug_index = ? 
				AND is_archived = TRUE
		)`

	return data.SelectExists(a.db, qry, slugIndex)
}

// InsertAlbum inserts an album record into the database.
func (a *albumAdapter) InsertAlbum(album api.AlbumRecord) error {

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

	return data.InsertRecord(a.db, qry, album)
}

// InsertAlbumImageXref inserts a xref record into the ablum_image table in the database.
func (a *albumAdapter) InsertAlbumImageXref(xref api.AlbumImageXref) error {

	qry := `
		INSERT INTO album_image (
			id,
			album_uuid,
			image_uuid,
			created_at
		) VALUES (?, ?, ?, ?)`

	return data.InsertRecord(a.db, qry, xref)
}

// UpdateAlbum updates an existing album record in the database.
// Note: not all fields are allowed to be updated.  Also, relevant fields need to be encrypted
// before calling this method.
func (a *albumAdapter) UpdateAlbum(album api.AlbumRecord) error {

	qry := `
		UPDATE album
		SET
			title = ?,
			description = ?,
			is_archived = ?,
			updated_at = ?
		WHERE slug_index = ?`

	return data.UpdateRecord(
		a.db,
		qry,
		album.Title,       // to update
		album.Description, // to update
		album.IsArchived,  // to update
		album.UpdatedAt,   // to update
		album.SlugIndex,   // where clause
	)
}
