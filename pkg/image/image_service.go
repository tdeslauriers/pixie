package image

import (
	"fmt"
	"log/slog"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
)

// Service is the interface for the image processing service.
// It defines methods that any image service must implement to handle image processing tasks.
// For example, fetching image db data and requesting signed URLs from the object storage service.
type Service interface {
	// GetImageData retrieves image data from the database along with a signed URL for the image.
	GetImageData(slug string) (*ImageData, error)
}

// NewService creates a new image service instance, returning a pointer to the concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor, obj storage.ObjectStorage) Service {
	return &imageService{
		sql:     sql,
		indexer: i,
		cryptor: c,
		store:   obj,
		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackageImage)).
			With(slog.String(util.ComponentKey, util.ComponentImage)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ Service = (*imageService)(nil)

type imageService struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor data.Cryptor
	store   storage.ObjectStorage

	logger *slog.Logger
}

// GetImageData is the concrete implementation of the interface method which
// retrieves image data from the database along with a signed URL for the image.
func (s *imageService) GetImageData(slug string) (*ImageData, error) {

	// TODO: Implement image record database retrieval logic

	// Generate a signed URL for the image using the object storage service
	// TODO: Replace with actual image object key/filename from database record
	signedURL, err := s.store.GetSignedUrl("2025/72be0c9b-6981-4a70-918b-715fba4280a3.jpg")
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to get signed URL for image %s: %v", slug, err))
		return nil, err
	}

	image := &ImageData{
		SignedUrl: signedURL.String(),
	}

	return image, nil
}
