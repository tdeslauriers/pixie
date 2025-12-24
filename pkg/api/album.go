package api

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
)

const (
	AlbumTitleMinLength = 2                          // Minimum length for image title
	AlbumTitleMaxLength = 32                         // Maximum length for image title
	AlbumTitleRegex     = `^[a-zA-Z0-9\-\/ ]{1,32}$` // Regex for image title, alphanumeric, spaces, max 64 chars

	AlbumDescriptionMinLength = 3                           // Minimum length for image description
	AlbumDescriptionMaxLength = 255                         // Maximum length for image description
	AlbumDescriptionRegex     = `^[\w\s.,!?'"()&-]{0,255}$` // Regex for image description, allows alphanumeric, spaces, punctuation, max 255 chars
)

// Album is a model which represents an album in the API response.
type Album struct {
	Csrf        string          `json:"csrf,omitempty"` // CSRF token, if required, or if present
	Id          string          `json:"id,omitempty"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Slug        string          `json:"slug,omitempty"`
	CreatedAt   data.CustomTime `json:"created_at,omitempty"`
	UpdatedAt   data.CustomTime `json:"updated_at,omitempty"`
	IsArchived  bool            `json:"is_archived"`
	Images      []ImageData     `json:"images,omitempty"` // image metadata records + their thumbnails signed urls
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

	if !validate.MatchesRegex(strings.TrimSpace(a.Title), AlbumTitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", AlbumTitleMinLength, AlbumTitleMaxLength)
	}

	// validate description
	if a.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Description), AlbumDescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", AlbumDescriptionMinLength, AlbumDescriptionMaxLength)
	}

	// validate slug
	if !validate.IsValidUuid(a.Slug) {
		return fmt.Errorf("invalid slug: %s", a.Slug)
	}

	return nil
}

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

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Title), AlbumTitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", AlbumTitleMinLength, AlbumTitleMaxLength)
	}

	// validate description
	if cmd.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Description), AlbumDescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", AlbumDescriptionMinLength, AlbumDescriptionMaxLength)
	}

	return nil
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

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Title), AlbumTitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", AlbumTitleMinLength, AlbumTitleMaxLength)
	}

	// validate description
	if cmd.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(cmd.Description), AlbumDescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", AlbumDescriptionMinLength, AlbumDescriptionMaxLength)
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

	if !validate.MatchesRegex(strings.TrimSpace(a.Title), AlbumTitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", AlbumTitleMinLength, AlbumTitleMaxLength)
	}

	// validate description
	if a.Description == "" {
		return fmt.Errorf("description is required")
	}

	if !validate.MatchesRegex(strings.TrimSpace(a.Description), AlbumDescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", AlbumDescriptionMinLength, AlbumDescriptionMaxLength)
	}

	// validate slug if present
	if a.Slug != "" && !validate.IsValidUuid(a.Slug) {
		return fmt.Errorf("invalid slug: %s", a.Slug)
	}

	return nil
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

// AlbumImageXref is a model which represents a record in the album_image cross-reference table.
type AlbumImageXref struct {
	Id        int             `db:"id"`
	AlbumId   string          `db:"album_uuid"`
	ImageId   string          `db:"image_uuid"`
	CreatedAt data.CustomTime `db:"created_at"`
}
