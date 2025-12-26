package album

import (
	"database/sql"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// StagedRepository is the interface for data operations related to staged albums and images.
type StagedRepository interface {

	// FindUnpublishedImages retrieves unpublished images.
	FindUnpublishedImages() ([]api.ImageRecord, error)
}

// NewStagedRepository creates a new instance of StagedRepository.
func NewStagedRepository(db *sql.DB) StagedRepository {
	// implementation details
	return &stagedAdapter{
		db: db,
	}
}

var _ StagedRepository = (*stagedAdapter)(nil) // compile-time interface check

// stagedAdapter is a concrete implementation of StagedRepository.
type stagedAdapter struct {
	db *sql.DB
}

// FindUnpublishedImages retrieves unpublished images.
func (s *stagedAdapter) FindUnpublishedImages() ([]api.ImageRecord, error) {

	qry := `
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
		FROM
			image i
		WHERE i.is_published = FALSE`

	return data.SelectRecords[api.ImageRecord](s.db, qry)
}
