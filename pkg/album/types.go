package album

import (
	"fmt"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/validate"
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
	Csrf          string          `json:"csrf,omitempty"` // CSRF token, if required, or if present
	Id            string          `json:"id,omitempty"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Slug          string          `json:"slug,omitempty"`
	CreatedAt     data.CustomTime `json:"created_at,omitempty"`
	UpdatedAt     data.CustomTime `json:"updated_at,omitempty"`
	IsArchived    bool            `json:"is_archived"`
	CoverImageUrl string          `json:"cover_image_url,omitempty"` // URL to the cover image, if any
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
