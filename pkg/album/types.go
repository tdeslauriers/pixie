package album

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/pkg/image"
)

const (
	TitleMinLength = 2                          // Minimum length for image title
	TitleMaxLength = 32                         // Maximum length for image title
	TitleRegex     = `^[a-zA-Z0-9\-\/ ]{1,32}$` // Regex for image title, alphanumeric, spaces, max 64 chars

	DescriptionMinLength = 3                           // Minimum length for image description
	DescriptionMaxLength = 255                         // Maximum length for image description
	DescriptionRegex     = `^[\w\s.,!?'"()&-]{0,255}$` // Regex for image description, allows alphanumeric, spaces, punctuation, max 255 chars

	ImageMaxSize = 10 * 1024 * 1024 // Maximum size for image file, 10 MB
)

// AddAlbumCmd is a a model which represents the command to add a new album record.
type AddAlbumCmd struct {
	Csrf string `json:"csrf"`

	Title       string `json:"title"`
	Description string `json:"description"`
	IsArchived  bool   `json:"is_archived"`
}

// Validate validates the AddAlbumCmd -> input validation.
func (cmd *AddAlbumCmd) Validate() error {

	// validate CSRF token, if present -> not always required
	if cmd.Csrf != "" && !validate.IsValidUuid(cmd.Csrf) {
		return fmt.Errorf("invalid CSRF token")
	}

	// validate title
	if cmd.Title == "" {
		return fmt.Errorf("title is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Title), TitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", TitleMinLength, TitleMaxLength)
	}

	// validate description
	if cmd.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Description), DescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", DescriptionMinLength, DescriptionMaxLength)
	}

	return nil
}

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

	if !validate.MatchesRegex(strings.TrimSpace(a.Title), TitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", TitleMinLength, TitleMaxLength)
	}

	// validate description
	if a.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Description), DescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", DescriptionMinLength, DescriptionMaxLength)
	}

	// validate slug if present
	if a.Slug != "" && !validate.IsValidUuid(a.Slug) {
		return fmt.Errorf("invalid slug: %s", a.Slug)
	}

	return nil
}

// Album is a model which represents an album in the API response.
type Album struct {
	Csrf        string            `json:"csrf,omitempty"` // CSRF token, if required, or if present
	Id          string            `json:"id,omitempty"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Slug        string            `json:"slug,omitempty"`
	CreatedAt   data.CustomTime   `json:"created_at,omitempty"`
	UpdatedAt   data.CustomTime   `json:"updated_at,omitempty"`
	IsArchived  bool              `json:"is_archived"`
	SignedUrl   string            `json:"signed_url,omitempty"` // URL to the cover image, if any
	Images      []image.ImageData `json:"images,omitempty"`     // image metadata records + their thumbnails signed urls
}

// Validate validates the Album -> input validation.
func (a *Album) Validate() error {

	// if csrf is present, validate it
	if a.Csrf != "" && !validate.IsValidUuid(a.Csrf) {
		return fmt.Errorf("invalid CSRF token")
	}

	// validate id
	if !validate.IsValidUuid(a.Id) {
		return fmt.Errorf("invalid album Id: %s", a.Id)
	}

	// validate title
	if a.Title == "" {
		return fmt.Errorf("title is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Title), TitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", TitleMinLength, TitleMaxLength)
	}

	// validate description
	if a.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Description), DescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", DescriptionMinLength, DescriptionMaxLength)
	}

	// validate slug
	if !validate.IsValidUuid(a.Slug) {
		return fmt.Errorf("invalid slug: %s", a.Slug)
	}

	return nil
}

// buildAlbumSQuery is a helper function which builds a query to
// retrieve album records based on the user's permissions.
// It uses the provided permissions map to create the query params.
func buildAlbumSQuery(ps map[string]permissions.PermissionRecord) (string, error) {

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
	if _, ok := ps["Gallery Curator"]; !ok {

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
	if _, ok := ps["Gallery Curator"]; !ok {
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

// buildAlbumImagesQuery is a helper function which builds a query to
// retrieve image records for a specific album based on the user's permissions.
func buildAlbumImagesQuery(ps map[string]permissions.PermissionRecord) (string, error) {

	// check for empty permissions map
	if len(ps) == 0 {
		return "", fmt.Errorf("permissions map cannot be empty for album images query builder")
	}

	// create album image select statement
	// Note: we use COALESCE to return empty strings for NULL values in the image fields
	var qb strings.Builder
	qb.WriteString(`
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
			LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid`)

	// add where clause to filter by album id and permissions uuids as variables/params
	qb.WriteString(`
		WHERE a.slug_index = ?`)

	if _, ok := ps["Gallery Curator"]; !ok {
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
	if _, ok := ps["Gallery Curator"]; !ok {
		// if not, add a condition to filter out archived images
		qb.WriteString(" AND a.is_archived = FALSE")
		qb.WriteString(" AND i.is_archived = FALSE")
		qb.WriteString(" AND i.is_published = TRUE")
	}

	return qb.String(), nil
}

// AlbumUpdateCmd is a model which represents the command to update an existing album record.
type AlbumUpdateCmd struct {
	Csrf        string `json:"csrf,omitempty"` // this may not always be required
	Title       string `json:"title"`
	Description string `json:"description"`
	IsArchived  bool   `json:"is_archived"`
}

func (cmd *AlbumUpdateCmd) Validate() error {

	// validate CSRF token, if present -> not always required
	if cmd.Csrf != "" && !validate.IsValidUuid(cmd.Csrf) {
		return fmt.Errorf("invalid CSRF token")
	}

	// validate title
	if cmd.Title == "" {
		return fmt.Errorf("title is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Title), TitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", TitleMinLength, TitleMaxLength)
	}

	// validate description
	if cmd.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Description), DescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", DescriptionMinLength, DescriptionMaxLength)
	}

	return nil
}
