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
	BlurUrl     string          `json:"blur_url,omitempty"` // URL to the cover image, if any
	Images      []ImageData     `json:"images,omitempty"`   // image metadata records + their thumbnails signed urls
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
