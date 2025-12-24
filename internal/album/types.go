package album

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// mapAlbumRecordsToApi is a helper function which maps album db records to album api structs
func MapAlbumRecordsToApi(albums []api.AlbumRecord) ([]api.Album, error) {
	if len(albums) == 0 {
		return nil, nil // no albums to map
	}

	apiAlbums := make([]api.Album, len(albums))
	for i, album := range albums {
		apiAlbum := api.Album{
			Id:          album.Id,
			Title:       album.Title,
			Description: album.Description,
			Slug:        album.Slug,
			CreatedAt:   album.CreatedAt,
			UpdatedAt:   album.UpdatedAt,
			IsArchived:  album.IsArchived,
		}
		apiAlbums[i] = apiAlbum
	}

	return apiAlbums, nil
}

// BuildAlbumSQuery is a helper function which builds a query to
// retrieve album records based on the user's permissions.
// It uses the provided permissions map to create the query params.
func BuildAlbumsQuery(ps map[string]permissions.PermissionRecord) (string, error) {

	// check for empty permissions map
	if len(ps) == 0 {
		return "", fmt.Errorf("permissions map cannot be empty for album query builder")
	}

	// create album select statement
	var qb strings.Builder
	qb.WriteString(`
		SELECT DISTINCT
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
			LEFT OUTER JOIN image i ON ai.image_uuid = i.uuid
			LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid`)

	// create the where clause based on the permissions uuids AS VARIABLES/PARAMS
	// Note: curator should see everything, so we don't filter by permissions if present
	if _, ok := ps["CURATOR"]; !ok {

		qb.WriteString(`
		WHERE ip.permission_uuid IN (`)
		i := 0
		for range ps {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString("?")
			i++
		}
		qb.WriteString(`)`)
	}

	// check if permissions include 'Gallery Curator'
	if _, ok := ps["CURATOR"]; !ok {
		// if not, add a condition to filter out albums that are archived
		qb.WriteString(" AND a.is_archived = FALSE")
		qb.WriteString(" AND i.is_archived = FALSE")
		qb.WriteString(" AND i.is_published = TRUE")
	}

	return qb.String(), nil
}

const AlbumImageQueryBase string = `
	SELECT DISTINCT
		a.uuid AS album_uuid,
		a.title AS album_title,
		a.description AS album_description,
		a.slug AS album_slug,
		a.created_at AS album_created_at,
		a.updated_at AS album_updated_at,
		a.is_archived AS album_is_archived,
		COALESCE(i.uuid, '') AS image_uuid,
		COALESCE(i.title, '') AS image_title,
		COALESCE(i.description, '') AS image_description,
		COALESCE(i.file_name,'') AS file_name,
		COALESCE(i.file_type, '') AS file_type,
		COALESCE(i.object_key, '') AS image_object_key,
		COALESCE(i.slug, '') AS image_slug,
		COALESCE(i.width, 0) AS width,
		COALESCE(i.height, 0) AS height,
		COALESCE(i.size, 0) AS size,
		COALESCE(i.image_date, '') AS image_date,
		COALESCE(i.created_at, '') AS image_created_at,
		COALESCE(i.updated_at, '') AS image_updated_at,
		COALESCE(i.is_archived, FALSE) AS image_is_archived,
		COALESCE(i.is_published, FALSE) AS image_is_published
	FROM album a
		LEFT OUTER JOIN album_image ai ON a.uuid = ai.album_uuid
		LEFT OUTER JOIN image i ON ai.image_uuid = i.uuid
		LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid`

// BuildAllAlbumsImagesQuery is a helper function which builds a query to
// retrieve album-image records based on the user's permissions.
// It uses the provided permissions map to create the query params.
func BuildAllAlbumsImagesQuery(ps map[string]permissions.PermissionRecord) (string, error) {

	// check for empty permissions map
	if len(ps) == 0 {
		return "", fmt.Errorf("permissions map cannot be empty for album query builder")
	}

	// create album select statement
	var qb strings.Builder
	qb.WriteString(AlbumImageQueryBase)

	// create the where clause based on the permissions uuids AS VARIABLES/PARAMS
	// Note: curator should see everything, so we don't filter by permissions if present
	if _, ok := ps["CURATOR"]; !ok {

		qb.WriteString(`
		WHERE ip.permission_uuid IN (`)
		i := 0
		for range ps {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString("?")
			i++
		}
		qb.WriteString(`)`)
	}

	// check if permissions include 'Gallery Curator'
	if _, ok := ps["CURATOR"]; !ok {
		// if not, add a condition to filter out albums that are archived
		qb.WriteString(" AND a.is_archived = FALSE")
		qb.WriteString(" AND i.is_archived = FALSE")
		qb.WriteString(" AND i.is_published = TRUE")
	}

	return qb.String(), nil
}

// BuildAlbumImagesQuery is a helper function which builds a query to
// retrieve image records for a specific album based on the user's permissions.
func BuildAlbumImagesQuery(ps map[string]permissions.PermissionRecord) (string, error) {

	// check for empty permissions map
	if len(ps) == 0 {
		return "", fmt.Errorf("permissions map cannot be empty for album images query builder")
	}

	// create album image select statement
	// Note: we use COALESCE to return empty strings for NULL values in the image fields
	var qb strings.Builder
	qb.WriteString(AlbumImageQueryBase)

	// add where clause to filter by album id and permissions uuids as variables/params
	qb.WriteString(`
		WHERE a.slug_index = ?`)

	if _, ok := ps["CURATOR"]; !ok {
		qb.WriteString(` AND ip.permission_uuid IN (`)
		i := 0
		for range ps {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString("?")
			i++
		}
		qb.WriteString(")")
	}

	// check if permissions include 'Gallery Curator'
	if _, ok := ps["CURATOR"]; !ok {
		// if not, add a condition to filter out archived images
		qb.WriteString(" AND a.is_archived = FALSE")
		qb.WriteString(" AND i.is_archived = FALSE")
		qb.WriteString(" AND i.is_published = TRUE")
	}

	return qb.String(), nil
}

// BuildAlbumExistsQuery builds a query to check if an album exists by its slug index.
func BuildAlbumExistsQuery() string {
	return `
		SELECT EXISTS (
			SELECT 1
			FROM album
			WHERE slug_index = ?
		)`
}

// BuildIsArchivedAlbumQuery builds a query to check if an album is archived by its slug index.
func BuildIsArchivedAlbumQuery() string {
	return `
		SELECT EXISTS (
			SELECT 1
			FROM album
			WHERE slug_index = ? 
				AND is_archived = TRUE
		)`
}
