package image

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

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

	// BuildPlaceholder builds the metadata for a placeholder image record.
	// eg, the id, slug, title, and description provided by the user.   The image processing
	// pipeline will build the rest of the record upon ingestion of the image file.
	BuildPlaceholder(r *ImageRecord) error
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
	obkey := "2025/" + slug + ".jpg"
	fmt.Printf("object key for image %s: %s\n", slug, obkey)
	signedURL, err := s.store.GetSignedUrl(obkey)
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to get signed URL for image %s: %v", slug, err))
		return nil, err
	}

	image := &ImageData{
		SignedUrl: signedURL.String(),
	}

	return image, nil
}

// BuildPlaceholder is the concrete implementation of the interface method which
// builds the metadata for a placeholder image record.
// eg, the id, slug, title, and description provided by the user.   The image processing
// pipeline will build the rest of the record upon ingestion of the image file.
func (s *imageService) BuildPlaceholder(r *ImageRecord) error {

	// validate the created metadata
	// should be a redundant check, but good practice
	if err := r.Validate(); err != nil {
		return fmt.Errorf("failed to validate image record: %v", err)
	}

	// encrypt the sensitive fields in the image record
	var (
		wg sync.WaitGroup

		titleCh = make(chan string, 1)
		descCh  = make(chan string, 1)
		slugCh  = make(chan string, 1)
		idxCh   = make(chan string, 1) // to capture the slug index for fast lookups, generated from the slug

		errCh = make(chan error, 4) // to capture any errors from encryption + slug index creation
	)

	wg.Add(3)
	go s.encrypt(r.Title, titleCh, errCh, &wg)
	go s.encrypt(r.Description, descCh, errCh, &wg)
	go s.encrypt(r.Slug, slugCh, errCh, &wg)

	wg.Add(1)
	go func() {
		defer wg.Done()

		// generate a blind index for the slug
		index, err := s.indexer.ObtainBlindIndex(r.Slug)
		if err != nil {
			s.logger.Error(fmt.Sprintf("failed to generate blind index for slug '%s': %v", r.Slug, err))
			errCh <- err
			return
		}
		idxCh <- index
	}()

	// wait for all goroutines to finish
	wg.Wait()
	close(titleCh)
	close(descCh)
	close(slugCh)
	close(idxCh)
	close(errCh)

	// check for any errors during encryption or index generation
	if len(errCh) > 0 {
		errs := make([]string, 0, len(errCh)+1)
		for err := range errCh {
			errs = append(errs, err.Error())
		}
		return fmt.Errorf("failed to encrypt image fields and/or generate slug index: %s", strings.Join(errs, "; "))
	}

	// need to make a copy of the image record to avoid modifying the original
	imageRecord := &ImageRecord{
		Id:          r.Id,
		Title:       <-titleCh,
		Description: <-descCh,
		FileName:    r.FileName,
		FileType:    r.FileType,
		ObjectKey:   r.ObjectKey,
		Slug:        <-slugCh,
		SlugIndex:   <-idxCh,
		Width:       r.Width,
		Height:      r.Height,
		Size:        r.Size,
		ImageDate:   r.ImageDate,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		IsArchived:  r.IsArchived,
		IsPublished: r.IsPublished,
	}

	// insert record into the database
	qry := `
	INSERT INTO images (
		uuid, 
		title,
		description,
		file_name,
		file_type,
		object_key,
		slug,
		slug_index,
		width,
		height,
		size,
		image_date,
		created_at,
		updated_at,
		is_archived,
		is_published
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := s.sql.InsertRecord(qry, imageRecord); err != nil {
		return fmt.Errorf("failed to insert image record into database: %v", err)
	}

	return nil
}

// encrypt is a helper function that encrypts the sensitive fields for the image service.
// mostly it exists so the code looks cleaner and more readable.
func (s *imageService) encrypt(plaintext string, encCh chan string, errCh chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	// encrypt the plaintext
	ciphertext, err := s.cryptor.EncryptServiceData([]byte(plaintext))
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to encrypt plaintext '%s': %v", plaintext, err))
		errCh <- err
		return
	}

	// send the ciphertext to the channel
	encCh <- ciphertext
}
