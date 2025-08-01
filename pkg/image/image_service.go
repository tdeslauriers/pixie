package image

import (
	"fmt"
	"log/slog"
	"net/url"
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

	// UpdateImageData updates an existing image record in the database.
	UpdateImageData(existing *ImageData, updated *ImageRecord) error

	// BuildPlaceholder builds the metadata for a placeholder image record.
	// eg, the id, slug, title, and description provided by the user.
	// Once meta data persisted, a presigned put url is generated and returned.
	// The image processing pipeline will build the rest of the record upon ingestion of the image file.
	BuildPlaceholder(r *ImageRecord) (*url.URL, error)
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

	// decrypt sensitive fields in the image record
	var (
		wg      sync.WaitGroup
		titleCh = make(chan string, 1)
		descCh  = make(chan string, 1)
		fnCh    = make(chan string, 1) // file name is encrypted because it is made of the slug
		okCh    = make(chan string, 1) // object key is encrypted because it is made of the slug
		slugCh  = make(chan string, 1)
		IdCh    = make(chan string, 1) // image date is encrypted

		errCh = make(chan error, 6) // to capture any errors from decryption
	)
	wg.Add(5)
	go s.decrypt(record.Title, "title", titleCh, errCh, &wg)
	go s.decrypt(record.Description, "description", descCh, errCh, &wg)
	go s.decrypt(record.FileName, "filename", fnCh, errCh, &wg)
	go s.decrypt(record.ObjectKey, "object key", okCh, errCh, &wg)
	go s.decrypt(record.Slug, "slug", slugCh, errCh, &wg)

	// may not exist yet or may have been an error in processing pipeline reading exif data
	if record.ImageDate != "" {
		wg.Add(1)
		go s.decrypt(record.ImageDate, "image date", IdCh, errCh, &wg)
	}

	// wait for all goroutines to finish
	wg.Wait()
	close(titleCh)
	close(descCh)
	close(fnCh)
	close(okCh)
	close(slugCh)
	close(IdCh)
	close(errCh)

	// check for any errors during decryption
	if len(errCh) > 0 {
		errs := make([]string, 0, len(errCh)+1)
		for err := range errCh {
			errs = append(errs, err.Error())
		}
		return nil, fmt.Errorf("failed to decrypt image data field value(s): %s", strings.Join(errs, "; "))
	}

	// Generate a signed URL for the image from object storage service
	objKey := <-okCh
	if objKey == "" {
		return nil, fmt.Errorf("object key for image '%s' is empty", slug)
	}

	signedURL, err := s.store.GetSignedUrl(objKey)
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
		Title:       <-titleCh,
		Description: <-descCh,
		FileName:    <-fnCh,
		FileType:    record.FileType,
		ObjectKey:   objKey, // channel was already read from to get signed URL
		Slug:        <-slugCh,
		Width:       record.Width,
		Height:      record.Height,
		Size:        record.Size,
		ImageDate:   <-IdCh, // fine if it is empty
		CreatedAt:   record.CreatedAt.String(),
		UpdatedAt:   record.UpdatedAt.String(),
		IsArchived:  record.IsArchived,
		IsPublished: record.IsPublished,

		SignedUrl: signedURL.String(),
	}

	return image, nil
}

// BuildPlaceholder is the concrete implementation of the interface method which
// builds the metadata for a placeholder image record.
// eg, the id, slug, title, and description provided by the user.
// Once meta data persisted, a presigned put url is generated and returned.
// The image processing pipeline will build the rest of the record upon ingestion of the image file.
func (s *imageService) BuildPlaceholder(r *ImageRecord) (*url.URL, error) {

	// validate the created metadata
	// should be a redundant check, but good practice
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate image record: %v", err)
	}

	// encrypt the sensitive fields in the image record
	var (
		wg sync.WaitGroup

		titleCh = make(chan string, 1)
		descCh  = make(chan string, 1)
		fnCh    = make(chan string, 1) // file name is encrypted because it is made of the slug
		okCh    = make(chan string, 1) // object key is encrypted because it is made of the slug
		slugCh  = make(chan string, 1)
		idxCh   = make(chan string, 1) // to capture the slug index for fast lookups, generated from the slug

		errCh = make(chan error, 6) // to capture any errors from encryption + slug index creation
	)

	wg.Add(5)
	go s.encrypt(r.Title, "title", titleCh, errCh, &wg)
	go s.encrypt(r.Description, "description", descCh, errCh, &wg)
	go s.encrypt(r.FileName, "file name", fnCh, errCh, &wg)
	go s.encrypt(r.ObjectKey, "object key", okCh, errCh, &wg)
	go s.encrypt(r.Slug, "slug", slugCh, errCh, &wg)

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
	close(fnCh)
	close(okCh)
	close(slugCh)
	close(idxCh)
	close(errCh)

	// check for any errors during encryption or index generation
	if len(errCh) > 0 {
		errs := make([]string, 0, len(errCh)+1)
		for err := range errCh {
			errs = append(errs, err.Error())
		}
		return nil, fmt.Errorf("failed to encrypt image fields and/or generate slug index: %s", strings.Join(errs, "; "))
	}

	// need to make a copy of the image record to avoid modifying the original
	imageRecord := ImageRecord{
		Id:          r.Id,
		Title:       <-titleCh,
		Description: <-descCh,
		FileName:    <-fnCh,
		FileType:    r.FileType,
		ObjectKey:   <-okCh,
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
	if err := s.sql.InsertRecord(qry, imageRecord); err != nil {
		return nil, fmt.Errorf("failed to insert image record into database: %v", err)
	}

	// generate a presigned put URL for the image file in object storage
	putUrl, err := s.store.GetPreSignedPutUrl(r.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get presigned put URL for image object key '%s': %v", r.ObjectKey, err)
	}
	return putUrl, nil
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
		return fmt.Errorf("failed to generate blind index for imaage slug '%s': %v", updated.Slug, err)
	}

	// encrypt the sensitive fields in the updated image record
	var (
		wg        sync.WaitGroup
		titleCh   = make(chan string, 1)
		descCh    = make(chan string, 1)
		imgDateCh = make(chan string, 1) // image date is encrypted to prevent leakage of sensitive information due to low numbers of images in certain years.
		objKeyCh  = make(chan string, 1) // object key may have changed if the image was unpublished or if the upload pipeline failed.
		errCh     = make(chan error, 4)  // to capture any errors from encryption
	)

	wg.Add(4)
	go s.encrypt(updated.Title, "title", titleCh, errCh, &wg)
	go s.encrypt(updated.Description, "description", descCh, errCh, &wg)
	go s.encrypt(updated.ImageDate, "image date", imgDateCh, errCh, &wg)
	go s.encrypt(updated.ObjectKey, "object key", objKeyCh, errCh, &wg)

	// wait for all goroutines to finish
	wg.Wait()
	close(titleCh)
	close(descCh)
	close(imgDateCh)
	close(objKeyCh) // need to close this channel even if it was not used
	close(errCh)

	// check for any errors during encryption
	if len(errCh) > 0 {
		errs := make([]string, 0, len(errCh)+1)
		for err := range errCh {
			errs = append(errs, err.Error())
		}
		return fmt.Errorf("failed to encrypt image fields: %s", strings.Join(errs, "; "))
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
	if err := s.sql.UpdateRecord(qry, <-titleCh, <-descCh, <-objKeyCh, <-imgDateCh, updated.UpdatedAt, updated.IsArchived, updated.IsPublished, index); err != nil {
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

// encrypt is a helper function that encrypts the sensitive fields for the image service.
// mostly it exists so the code looks cleaner and more readable.
func (s *imageService) encrypt(plaintext, fieldname string, encCh chan string, errCh chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	// encrypt the plaintext
	ciphertext, err := s.cryptor.EncryptServiceData([]byte(plaintext))
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to encryp %s '%s': %v", fieldname, plaintext, err))
		errCh <- err
		return
	}

	// send the ciphertext to the channel
	encCh <- ciphertext
}

// decrypt is a helper function that decrypts the sensitive fields for the image service.
func (s *imageService) decrypt(ciphertext, fieldname string, decCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	// decrypt the ciphertext
	plaintext, err := s.cryptor.DecryptServiceData(ciphertext)
	if err != nil {
		s.logger.Error(fmt.Sprintf("failed to decrypt %s: %v", fieldname, err))
		errCh <- err
		return
	}

	// send the plaintext to the channel
	decCh <- string(plaintext)
}
