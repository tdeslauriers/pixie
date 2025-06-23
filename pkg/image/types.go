package image

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/validate"
)

const (
	TitleMinLength = 3                      // Minimum length for image title
	TitleMaxLength = 64                     // Maximum length for image title
	TitleRegex     = `^[a-zA-Z0-9 ]{1,64}$` // Regex for image title, alphanumeric, spaces, max 64 chars

	DescriptionMinLength = 3                           // Minimum length for image description
	DescriptionMaxLength = 255                         // Maximum length for image description
	DescriptionRegex     = `^[\w\s.,!?'"()&-]{0,255}$` // Regex for image description, allows alphanumeric, spaces, punctuation, max 255 chars

	ImageMaxSize = 10 * 1024 * 1024 // Maximum size for image file, 10 MB
)

var AllowedFileTypes = []string{
	"image/jpeg",    // JPEG image format
	"image/png",     // PNG image format
	"image/gif",     // GIF image format
	"image/webp",    // WebP image format
	"image/tiff",    // TIFF image format
	"image/bmp",     // BMP image format
	"image/svg+xml", // SVG image format
}

var extensionMap = map[string]string{
	"image/jpeg":    "jpg",
	"image/png":     "png",
	"image/gif":     "gif",
	"image/webp":    "webp",
	"image/tiff":    "tiff",
	"image/bmp":     "bmp",
	"image/svg+xml": "svg",
}

// ImageData is a composite model that includes both the necessary fields from database record
// but also the signed url from the object storage service and other metadata.
// It is used to return image data to the client in a single response.
type ImageData struct {
	Id          string `db:"uuid" json:"id,omitempty"`               // Unique identifier for the image record
	Title       string `db:"title" json:"title"`                     // Title of the image
	Description string `db:"description" json:"description"`         // Description of the image
	FileName    string `db:"file_name" json:"file_name,omitempty"`   // name of the file with it's extension, eg, "slug.jpg"   // MIME type of the image, eg, "image/jpeg", "image/png"
	FileType    string `db:"file_type" json:"file_type,omitempty"`   // MIME type of the image, eg, "image/jpeg", "image/png"
	ObjectKey   string `db:"object_key" json:"object_key,omitempty"` // The key used to store the image in object storage, eg, "2025/slug.jpg"
	Slug        string `db:"slug" json:"slug,omitempty"`             // ENCRYPTED: a unique slug for the image, used in URLs
	Width       int    `db:"width" json:"width,omitempty"`           // Width of the image in pixels
	Height      int    `db:"height" json:"height,omitempty"`         // Height of the image in pixels
	Size        int64  `db:"size" json:"size,omitempty"`             // Size of the image file in bytes
	ImageDate   string `db:"image_date" json:"image_date,omitempty"` // Date when the image was taken or created, ie, from exif metadata
	CreatedAt   string `db:"created_at" json:"created_at,omitempty"` // Timestamp when the image was created
	UpdatedAt   string `db:"updated_at" json:"updated_at,omitempty"` // Timestamp when the image was last updated
	IsArchived  bool   `db:"is_archived" json:"is_archived"`         // Indicates if the image is archived
	IsPublished bool   `db:"is_published" json:"is_published"`       // Indicates if the image is published and visible to users

	// can be either the pre-signed PUT URL for uploading the image file
	// or the pre-signed GET URL for downloading the image file.
	// This field is dynamically generated and not stored in the database.
	// NOTE: may need to break this model out into two separate models later
	SignedUrl string `json:"signed_url,omitempty"` // The signed URL for the image, used to access the image in object storage
}

// ImageRecord is a model that represents the image record in the database.
// It contains the fields that are stored in the database, such as the image slug,
// metadata, and any other relevant information.
// It does not include the signed URL, as that is generated dynamically when requested.
type ImageRecord struct {
	Id          string `db:"uuid" json:"id"`                   // Unique identifier for the image record
	Title       string `db:"title" json:"title"`               // ENCRYPTED: title of the image
	Description string `db:"description" json:"description"`   // ENCRYPTED: description of the image
	FileName    string `db:"file_name" json:"file_name"`       // name of the file with it's extension, eg, "slug.jpg"
	FileType    string `db:"file_type" json:"file_type"`       // MIME type of the image, eg, "jpeg"
	ObjectKey   string `db:"object_key" json:"object_key"`     // The key used to store the image in object storage, eg, "2025/slug.jpg"
	Slug        string `db:"slug" json:"slug"`                 // ENCRYPTED: a unique slug for the image, used in URLs
	SlugIndex   string `db:"slug_index" json:"slug_index"`     // blind index for slug, indexed for fast lookups
	Width       int    `db:"width" json:"width"`               // Width of the image in pixels
	Height      int    `db:"height" json:"height"`             // Height of the image in pixels
	Size        int64  `db:"size" json:"size"`                 // Size of the image file in bytes
	ImageDate   string `db:"image_date" json:"image_date"`     // ENCRYPTED: date when the image was taken or created, ie, from exif metadata
	CreatedAt   string `db:"created_at" json:"created_at"`     // Timestamp when the image was created
	UpdatedAt   string `db:"updated_at" json:"updated_at"`     // Timestamp when the image was last updated
	IsArchived  bool   `db:"is_archived" json:"is_archived"`   // Indicates if the image is archived
	IsPublished bool   `db:"is_published" json:"is_published"` // Indicates if the image is published and visible to users
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
	if !validate.MatchesRegex(strings.TrimSpace(r.Title), TitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", TitleMinLength, TitleMaxLength)
	}

	// validate the description
	if !validate.MatchesRegex(strings.TrimSpace(r.Description), DescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", DescriptionMinLength, DescriptionMaxLength)
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
	allowed := false
	for _, allowedType := range AllowedFileTypes {
		if strings.TrimSpace(r.FileType) == allowedType {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("file type must be one of: %s", strings.Join(AllowedFileTypes, ", "))
	}

	// validate the size
	if r.Size <= 0 || r.Size > ImageMaxSize {
		return fmt.Errorf("image size must be greater than 0 and less than or equal to %d bytes", ImageMaxSize)
	}

	// width and height are optional, but if provided, they must be positive integers
	if r.Width < 0 {
		return fmt.Errorf("image width must be a positive integer")
	}

	if r.Height < 0 {
		return fmt.Errorf("image height must be a positive integer")
	}

	// all the date fields will be set programmatically, so we don't validate them here

	return nil
}

// AddMetaDataCmd is a command that adds metadata to an image record.
// model represents the incoming data (title, description, filetype, size) for an image reocord to
// be created and the object key set up in the object storage service., it will not be used by pixie, but may need to be set by other servies  using this pacakge.
// Note: csrf will not be used by pixie, but may need to be set by other servies  using this pacakge.
type AddMetaDataCmd struct {
	Csrf string `json:"csrf,omitempty"` // CSRF token for security

	Title       string `json:"title"`       // Title of the image
	Description string `json:"description"` // Description of the image
	FileType    string `json:"file_type"`   // MIME type of the image, eg, "image/jpeg", "image/png"
	Size        int64  `json:"size"`        // Size of the image file in bytes
}

// Validate checks the AddMetaDataCmd for valid data.
// It ensures that the title, description, file type, and size meet the specified criteria.
func (cmd *AddMetaDataCmd) Validate() error {

	// validate the csrf token
	// csrf will not always be present in cmd, so we check if it's not nil before validating
	if cmd.Csrf != "" {
		if !validate.IsValidUuid(cmd.Csrf) {
			return fmt.Errorf("csrf token must be a valid UUID")
		}
	}

	// validate the title
	if !validate.MatchesRegex(strings.TrimSpace(cmd.Title), TitleRegex) {
		return fmt.Errorf("title must be alphanumeric and spaces, min %d chars, max %d chars", TitleMinLength, TitleMaxLength)
	}

	// validate the description
	if !validate.MatchesRegex(strings.TrimSpace(cmd.Description), DescriptionRegex) {
		return fmt.Errorf("description must be alphanumeric, spaces, and punctuation, min %d chars, max %d chars", DescriptionMinLength, DescriptionMaxLength)
	}

	// validate the file type
	allowed := false
	for _, allowedType := range AllowedFileTypes {
		if strings.TrimSpace(cmd.FileType) == allowedType {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("file type must be one of: %s", strings.Join(AllowedFileTypes, ", "))
	}

	// validate the size
	if cmd.Size <= 0 || cmd.Size > ImageMaxSize {
		return fmt.Errorf("image size must be greater than 0 and less than or equal to %d bytes", ImageMaxSize)
	}

	return nil
}

// GetExtension returns the file extension based on the MIME type.
func (cmd *AddMetaDataCmd) GetExtension() (string, error) {
	// Check if the file type is in the extension map
	if ext, ok := extensionMap[strings.TrimSpace(cmd.FileType)]; ok {
		return ext, nil
	}
	// If not found, return an error
	return "", fmt.Errorf("unsupported file type: %s", cmd.FileType)
}
