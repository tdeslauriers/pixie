package db

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// AlbumRecord is a model which represents an album record in the database.
type AlbumRecord struct {
	Id          string          `db:"uuid" json:"id,omitempty"`
	Title       string          `db:"title" json:"title"`             // encrypted
	Description string          `db:"description" json:"description"` // encrypted
	Slug        string          `db:"slug" json:"slug,omitempty"`     // encrypted
	SlugIndex   string          `db:"slug_index" json:"slug_index,omitempty"`
	CreatedAt   data.CustomTime `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt   data.CustomTime `db:"updated_at" json:"updated_at,omitempty"`
	IsArchived  bool            `db:"is_archived" json:"is_archived"`
}

// Validate validates the AlbumRecord -> input validation.
func (a *AlbumRecord) Validate() error {

	// validate id if present
	if a.Id != "" && !validate.IsValidUuid(a.Id) {
		return fmt.Errorf("invalid album ID: %s", a.Id)
	}

	// validate title
	if a.Title == "" {
		return fmt.Errorf("title is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Title), api.AlbumTitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", api.AlbumTitleMinLength, api.AlbumTitleMaxLength)
	}

	// validate description
	if a.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Description), api.AlbumDescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", api.AlbumDescriptionMinLength, api.AlbumDescriptionMaxLength)
	}

	// validate slug if present
	if a.Slug != "" && !validate.IsValidUuid(a.Slug) {
		return fmt.Errorf("invalid slug: %s", a.Slug)
	}

	return nil
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
	SELECT
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

// AlbumImageRecord is a model which represents a row of data from a JOIN query between
// the album, album_image, and image tables.
type AlbumImageRecord struct {
	AlbumId          string          `db:"album_uuid"`
	AlbumTitle       string          `db:"album_title"`       // encrypted
	AlbumDescription string          `db:"album_description"` // encrypted
	AlbumSlug        string          `db:"album_slug"`        // encrypted
	AlbumCreatedAt   data.CustomTime `db:"album_created_at"`
	AlbumUpdatedAt   data.CustomTime `db:"album_updated_at"`
	AlbumIsArchived  bool            `db:"album_is_archived"`
	ImageId          string          `db:"image_uuid"`         // Unique identifier for the image record
	ImageTitle       string          `db:"image_title"`        // encrypted: title of the image
	ImageDescription string          `db:"image_description"`  // encrypted: description of the image
	FileName         string          `db:"file_name"`          // name of the file with it's extension, eg, "slug.jpg"
	FileType         string          `db:"file_type"`          // MIME type of the image, eg, "jpeg"
	ObjectKey        string          `db:"object_key"`         // The key used to store the image in object storage, eg, "2025/slug.jpg"
	ImageSlug        string          `db:"image_slug"`         // encrypted: a unique slug for the image, used in URLs
	Width            int             `db:"width"`              // Width of the image in pixels
	Height           int             `db:"height"`             // Height of the image in pixels
	Size             int64           `db:"size"`               // Size of the image file in bytes
	ImageDate        string          `db:"image_date"`         // encrypted: date when the image was taken or created, ie, from exif metadata
	ImageCreatedAt   string          `db:"image_created_at"`   // Timestamp when the image was created
	ImageUpdatedAt   string          `db:"image_updated_at"`   // Timestamp when the image was last updated
	ImageIsArchived  bool            `db:"image_is_archived"`  // Indicates if the image is archived
	ImageIsPublished bool            `db:"image_is_published"` // Indicates if the image is published and visible to users
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
		qb.WriteString(`AND ip.permission_uuid IN (`)
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

// AlbumImageXref is a model which represents a record in the album_image cross-reference table.
type AlbumImageXref struct {
	Id        int             `db:"id"`
	AlbumId   string          `db:"album_uuid"`
	ImageId   string          `db:"image_uuid"`
	CreatedAt data.CustomTime `db:"created_at"`
}
