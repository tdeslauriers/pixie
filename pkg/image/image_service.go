package image

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
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

	// UpdateImageData updates an existing image record in the database.
	UpdateImageData(existing *ImageData, updated *ImageRecord) error

	// BuildPlaceholder builds the metadata for a placeholder image record.
	// eg, the id, slug, title, and description provided by the user.
	// Once meta data persisted, a presigned put url is generated and returned.
	// The image processing pipeline will build the rest of the record upon ingestion of the image file.
	BuildPlaceholder(cmd AddMetaDataCmd) (*ImageData, error)
}

// NewService creates a new image service instance, returning a pointer to the concrete implementation.
func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor, obj storage.ObjectStorage) Service {
	return &imageService{
		sql:     sql,
		indexer: i,
		cryptor: NewCryptor(c),
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
	cryptor Cryptor // image data specific wrapper around data.Cryptor
	store   storage.ObjectStorage

	logger *slog.Logger
}

// GetImageData is the concrete implementation of the interface method which
// retrieves image data from the database along with a signed URL for the image.
func (s *imageService) GetImageData(slug string) (*ImageData, error) {

	// get blind index for the slug
	index, err := s.indexer.ObtainBlindIndex(slug)
	if err != nil {
		return nil, fmt.Errorf("failed to generate blind index for image slug '%s': %v", slug, err)
	}

	// get image record (metadata) from the database using the slug index
	qry := `
		SELECT 
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
		FROM image 
		WHERE slug_index = ?`
	var record ImageRecord
	if err := s.sql.SelectRecord(qry, &record, index); err != nil {
		return nil, fmt.Errorf("failed to select image record for slug '%s': %v", slug, err)
	}

	// decrypt the sensitive fields in the image record
	if err := s.cryptor.DecryptImageRecord(&record); err != nil {
		return nil, fmt.Errorf("failed to decrypt image record for slug '%s': %v", slug, err)
	}

	// Generate a signed URL for the image from object storage service
	if record.ObjectKey == "" {
		return nil, fmt.Errorf("object key for image '%s' is empty", slug)
	}

	// object key has been decrypted above
	signedURL, err := s.store.GetSignedUrl(record.ObjectKey)
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to get signed URL for image %s: %v", slug, err))
		return nil, err
	}

	// if the signed URL is empty, return an error
	if signedURL == nil || signedURL.String() == "" {
		return nil, fmt.Errorf("failed to get signed URL for image %s: URL is empty", slug)
	}

	// create the ImageData struct to return
	image := &ImageData{
		Id:          record.Id,
		Title:       record.Title,
		Description: record.Description,
		FileName:    record.FileName,
		FileType:    record.FileType,
		ObjectKey:   record.ObjectKey,
		Slug:        record.Slug,
		Width:       record.Width,
		Height:      record.Height,
		Size:        record.Size,
		ImageDate:   record.ImageDate, // possibly empty, which is fine.
		CreatedAt:   record.CreatedAt.String(),
		UpdatedAt:   record.UpdatedAt.String(),
		IsArchived:  record.IsArchived,
		IsPublished: record.IsPublished,

		SignedUrl: signedURL.String(),
	}

	return image, nil
}

// BuildPlaceholder is the concrete implementation of the interface method which
// builds the metadata for a placeholder image from an add image cmd.
// The image processing pipeline will build the rest of the record upon ingestion of the image file.
func (s *imageService) BuildPlaceholder(cmd AddMetaDataCmd) (*ImageData, error) {

	// validate the created metadata
	// should be a redundant check, but good practice
	if err := cmd.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate image record: %v", err)
	}

	// notification from the object storage service
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate new image record id: %v", err)
	}

	// create the slug (which is a unique identifier shared as the filename in object storage)
	slug, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate slug for new image record: %v", err)
	}

	// get file type from the command --> extension
	ext, err := cmd.GetExtension()
	if err != nil {
		return nil, fmt.Errorf("failed to get file extension from command: %v", err)
	}

	// build the filename for the image file
	// this should not change even if the namespace changes in the object storage service
	fileName := fmt.Sprintf("%s.%s", slug.String(), ext)

	// build the object key for the image file in object storage
	objectKey := fmt.Sprintf("uploads/%s", fileName)

	now := time.Now().UTC()

	// incomplete/stubbed image record missing fields that will be filled in later
	// when the image file is processed and the object storage service notifies the image service
	record := ImageRecord{
		Id:          id.String(),
		Title:       strings.TrimSpace(cmd.Title),
		Description: strings.TrimSpace(cmd.Description),
		FileName:    fileName,
		FileType:    strings.TrimSpace(cmd.FileType),
		ObjectKey:   objectKey,
		Slug:        slug.String(),
		Size:        cmd.Size,
		Width:       0, // default to 0 until image is processed
		Height:      0, // default to 0 until image is processed
		CreatedAt:   data.CustomTime{Time: now},
		UpdatedAt:   data.CustomTime{Time: now}, // updated at is the same as created at for a new record
		IsArchived:  false,                      // default to not archived
		IsPublished: false,                      // default to not published --> image prcessing pipeline will publish the image when processing is complete
	}

	// get the blind index for the slug
	index, err := s.indexer.ObtainBlindIndex(record.Slug)
	if err != nil {
		return nil, fmt.Errorf("failed to generate blind index for image slug '%s': %v", record.Slug, err)
	}

	// set the slug index for the image record
	record.SlugIndex = index

	// create a copy of the record to avoid modifying the original
	copy := record

	// encrypt the sensitive fields in the copy of the image record
	if err := s.cryptor.EncryptImageRecord(&copy); err != nil {
		return nil, fmt.Errorf("failed to encrypt image record '%s': %v", record.Id, err)
	}

	// insert record into the database
	qry := `
	INSERT INTO image (
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
	if err := s.sql.InsertRecord(qry, copy); err != nil {
		return nil, fmt.Errorf("failed to insert image record into database: %v", err)
	}

	// generate a presigned put URL for the image file in object storage
	putUrl, err := s.store.GetPreSignedPutUrl(record.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get presigned put URL for image object key '%s': %v", record.ObjectKey, err)
	}

	// build image meta data struct to return
	data := &ImageData{
		Id:          record.Id,
		Title:       record.Title,
		Description: record.Description,
		FileName:    record.FileName,
		FileType:    record.FileType,
		ObjectKey:   record.ObjectKey, // this is the "uploads/slug.jpg" key in object storage -> staging
		Slug:        record.Slug,
		Size:        record.Size,
		CreatedAt:   record.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   record.UpdatedAt.Format(time.RFC3339), // format the time as RFC3339
		IsArchived:  record.IsArchived,
		IsPublished: record.IsPublished,
		SignedUrl:   putUrl.String(), // the pre-signed PUT URL for the browser to upload the image file into object storage
	}
	return data, nil
}

// UpdateImageData is the concrete implementation of the interface method which
// updates an existing image record in the database.
// NOTE: the only reason existing is passed is so that we check if the ojbectstore needs to be updated
// due to a new ojbect key being generated.
func (s *imageService) UpdateImageData(existing *ImageData, updated *ImageRecord) error {

	// validate updated image record
	// redundant check, but good practice
	if err := updated.Validate(); err != nil {
		return fmt.Errorf("failed to validate updated image record: %v", err)
	}

	// is update necessary?
	if existing.Title == updated.Title &&
		existing.Description == updated.Description &&
		existing.ImageDate == updated.ImageDate &&
		existing.ObjectKey == updated.ObjectKey &&
		existing.IsArchived == updated.IsArchived &&
		existing.IsPublished == updated.IsPublished {
		s.logger.Info(fmt.Sprintf("no changes detected for image slug '%s', skipping update", existing.Slug))
		return nil // no changes, nothing to update
	}

	// get the blind index for the slug
	// using existing slug since this should not be updated generally..
	// if it needs to be updated, the slug should be specifically changed by a separate method.
	index, err := s.indexer.ObtainBlindIndex(existing.Slug)
	if err != nil {
		return fmt.Errorf("failed to generate blind index for image slug '%s': %v", updated.Slug, err)
	}

	// encrypt the sensitive fields in the updated image record
	if err := s.cryptor.EncryptImageRecord(updated); err != nil {
		return fmt.Errorf("failed to encrypt updated image data for slug '%s': %v", updated.Slug, err)
	}

	// update the image record in the database
	// NOTE: more fields can be added here as needed
	qry := `
		UPDATE image SET
			title = ?,
			description = ?,
			object_key = ?,
			image_date = ?,
			updated_at = ?,
			is_archived = ?,
			is_published = ?
		WHERE slug_index = ?`
	if err := s.sql.UpdateRecord(
		qry,
		updated.Title,
		updated.Description,
		updated.ObjectKey,
		updated.ImageDate,
		updated.UpdatedAt,
		updated.IsArchived,
		updated.IsPublished,
		index); err != nil {
		return fmt.Errorf("failed to update image record in database: %v", err)
	}

	// if the object key has changed, we need to update the object storage service
	if updated.ObjectKey != existing.ObjectKey {
		if err := s.store.MoveObject(existing.ObjectKey, updated.ObjectKey); err != nil {
			return fmt.Errorf("failed to move image from '%s' to '%s' in object storage: %v", existing.ObjectKey, updated.ObjectKey, err)
		}
		s.logger.Info(fmt.Sprintf("image slug '%s' moved in object storage from '%s' to '%s'", updated.Slug, existing.ObjectKey, updated.ObjectKey))
	}

	return nil
}
