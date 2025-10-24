package db

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// ImageRecord is a model that represents the image record in the database.
// It contains the fields that are stored in the database, such as the image slug,
// metadata, and any other relevant information.
// It does not include the signed URL, as that is generated dynamically when requested.
type ImageRecord struct {
	Id          string          `db:"uuid" json:"id"`                   // Unique identifier for the image record
	Title       string          `db:"title" json:"title"`               // ENCRYPTED: title of the image
	Description string          `db:"description" json:"description"`   // ENCRYPTED: description of the image
	FileName    string          `db:"file_name" json:"file_name"`       // name of the file with it's extension, eg, "slug.jpg"
	FileType    string          `db:"file_type" json:"file_type"`       // MIME type of the image, eg, "jpeg"
	ObjectKey   string          `db:"object_key" json:"object_key"`     // The key used to store the image in object storage, eg, "2025/slug.jpg"
	Slug        string          `db:"slug" json:"slug"`                 // ENCRYPTED: a unique slug for the image, used in URLs
	SlugIndex   string          `db:"slug_index" json:"slug_index"`     // blind index for slug, indexed for fast lookups
	Width       int             `db:"width" json:"width"`               // Width of the image in pixels
	Height      int             `db:"height" json:"height"`             // Height of the image in pixels
	Size        int64           `db:"size" json:"size"`                 // Size of the image file in bytes
	ImageDate   string          `db:"image_date" json:"image_date"`     // ENCRYPTED: date when the image was taken or created, ie, from exif metadata
	CreatedAt   data.CustomTime `db:"created_at" json:"created_at"`     // Timestamp when the image was created
	UpdatedAt   data.CustomTime `db:"updated_at" json:"updated_at"`     // Timestamp when the image was last updated
	IsArchived  bool            `db:"is_archived" json:"is_archived"`   // Indicates if the image is archived
	IsPublished bool            `db:"is_published" json:"is_published"` // Indicates if the image is published and visible to users
}

// Validate checks the ImageRecord for valid data before storing it in the database.
// NOTE: regexes are for plaintext validation, not for encrypted fields.
func (r *ImageRecord) Validate() error {

	// validate the ID
	if r.Id != "" {
		if !validate.IsValidUuid(r.Id) {
			return fmt.Errorf("id must be a valid UUID")
		}
	}

	// validate the title
	if !validate.MatchesRegex(strings.TrimSpace(r.Title), api.ImageTitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", api.ImageTitleMinLength, api.ImageTitleMaxLength)
	}

	// validate the description
	if !validate.MatchesRegex(strings.TrimSpace(r.Description), api.ImageDescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", api.ImageDescriptionMinLength, api.ImageDescriptionMaxLength)
	}

	// validate the file name
	if r.FileName != "" {
		split := strings.Split(r.FileName, ".")
		if len(split) < 2 || len(split[len(split)-1]) == 0 {
			return fmt.Errorf("file name must include a valid file extension, eg, 'slug.jpg'")
		}

		if !validate.IsValidUuid(split[0]) {
			return fmt.Errorf("file name must start with a valid UUID, eg, 'xxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.jpg'")
		}
	}

	// Validate the slug
	if !validate.IsValidUuid(r.Slug) {
		return fmt.Errorf("slug must be a valid UUID")
	}

	// validate the file type
	if !IsValidFiletype(r.FileType) {
		return fmt.Errorf("file type must be one of: %s", strings.Join(api.AllowedFileTypes, ", "))
	}

	// validate the size
	if r.Size <= 0 || r.Size > api.ImageMaxSize {
		return fmt.Errorf("image size must be greater than 0 and less than or equal to %d bytes", api.ImageMaxSize)
	}

	// width and height are optional, but if provided, they must be positive integers
	if r.Width < 0 {
		return fmt.Errorf("image width must be a positive integer")
	}

	if r.Height < 0 {
		return fmt.Errorf("image height must be a positive integer")
	}

	

	return nil
}

// IsValidFiletype checks if the provided file type is allowed from a list of accepted types.
func IsValidFiletype(fileType string) bool {
	allowed := false
	for _, allowedType := range api.AllowedFileTypes {
		if strings.TrimSpace(fileType) == allowedType {
			allowed = true
			break
		}
	}
	return allowed
}

type ImagePermissionXref struct {
	Id           int             `db:"id" json:"id"`                         // Unique identifier for the xref record
	ImageId      string          `db:"image_uuid" json:"image_id"`           // UUID of the image
	PermissionId string          `db:"permission_uuid" json:"permission_id"` // UUID of the permission
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at"`         // Timestamp when the xref was created
}

// BuildGetImageQuery builds the SQL query to get an image by its slug and the user's permissions.
// It returns the query string and any parameters to be used in the query.
func BuildGetImageQuery(userPs map[string]permissions.PermissionRecord) string {

	var qb strings.Builder

	baseQry := `
		SELECT 
			i.uuid,
			i.title,
			i.description,
			i.file_name,
			i.file_type,
			i.object_key,
			i.slug,
			i.slug_index,
			i.width,
			i.height,
			i.size,
			i.image_date,
			i.created_at,
			i.updated_at,
			i.is_archived,
			i.is_published 
		FROM image i
			LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
			LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
		WHERE i.slug_index = ?`
	qb.WriteString(baseQry)

	// check if the user is a curator (admin)
	// if they are, then return the base query without any additional filters
	if _, ok := userPs[util.PermissionCurator]; ok {
		return qb.String()
	}

	// if the user is not a curator, need to add permission filters
	// makes a (?, ?, ?) list for the IN clause the length of the user's permissions
	if len(userPs) > 0 {
		qb.WriteString(" AND p.uuid IN (")
		for i := 0; i < len(userPs); i++ {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString(` ?`)
		}
		qb.WriteString(`)`)
	}

	// if the user is not a curator, they can only see published, non-archived images
	qb.WriteString(" AND i.is_published = TRUE")
	qb.WriteString(" AND i.is_archived = FALSE")

	return qb.String()
}

// BuildImageExistsQry builds an SQL query to check if an image exists based on its slug.
// It returns the query string and any parameters to be used in the query.
// Note: at the moment, no params are needed or constructed, but this may change in the future.
func BuildImageExistsQry() string {
	var qb strings.Builder

	// base query to check if the image exists
	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
			WHERE i.slug_index = ?`
	qb.WriteString(baseQry)

	qb.WriteString(")")

	return qb.String()
}

// BuildImagePermissionsQry builds an sql query to check if an image exists but the user does not have permissions.
func BuildImagePermissionsQry(userPs map[string]permissions.PermissionRecord) string {
	var qb strings.Builder

	// base query to check if the image exists
	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
				LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
				LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
			WHERE i.slug_index = ?`
	qb.WriteString(baseQry)

	// exclude images that have any of the user's permissions
	if len(userPs) > 0 {
		qb.WriteString(" AND (p.uuid IS NULL OR p.uuid NOT IN (")
		for i := 0; i < len(userPs); i++ {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString(` ?`)
		}
		qb.WriteString("))")
	}

	qb.WriteString(")")

	return qb.String()
}

// BuildImageArchivedQry builds an SQL query to check if an image is archived.
// It returns the query string and any parameters to be used in the query.
// Note: at the moment, no params are needed or constructed, but this may change in the future.
func BuildImageArchivedQry() string {
	var qb strings.Builder

	// base query to check if the image is archived
	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
				LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
				LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
			WHERE i.slug_index = ?
				AND i.is_archived = TRUE`
	qb.WriteString(baseQry)

	return qb.String()
}

// BuildImagePublishedQry builds an SQL query to check if an image is published.
// It returns the query string and any parameters to be used in the query.
// Note: at the moment, no params are needed or constructed, but this may change in the future.
func BuildImagePublishedQry() string {
	var qb strings.Builder

	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
				LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
				LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
			WHERE i.slug_index = ?
				AND i.is_published = FALSE`
	qb.WriteString(baseQry)

	return qb.String()
}
