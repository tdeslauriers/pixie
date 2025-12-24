package picture

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/crypt"
	"github.com/tdeslauriers/pixie/internal/pipeline"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// ImageService is the interface for the image processing service.
// It defines methods that any image service must implement to handle image processing tasks.
// For example, fetching image db data and requesting signed URLs from the object storage service.
type ImageService interface {

	// GetImageData retrieves image data from the database (based on the user's permissions) and
	// fetches signed URL for the image.
	GetImageData(slug string, userPs map[string]permissions.PermissionRecord) (*api.ImageData, error)

	// UpdateImageData updates an existing image record in the database.
	UpdateImageData(ctx context.Context, existing *api.ImageData, updated *api.ImageRecord) error

	// BuildPlaceholder builds the metadata for a placeholder image record.
	// eg, the id, slug, title, and description provided by the user.
	// Once meta data persisted, a presigned put url is generated and returned.
	// The image processing pipeline will build the rest of the record upon ingestion of the image file.
	BuildPlaceholder(cmd api.AddMetaDataCmd) (*api.Placeholder, error)
}

// NewImageService creates a new image service instance, returning a pointer to the concrete implementation.
func NewImageService(
	sql data.SqlRepository,
	i data.Indexer, c data.Cryptor,
	obj storage.ObjectStorage,
	q chan pipeline.ReprocessCmd,
) ImageService {

	return &imageService{
		sql:       sql,
		indexer:   i,
		cryptor:   crypt.NewCryptor(c),
		store:     obj,
		reprocess: q,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ComponentKey, util.ComponentImage)),
	}
}

var _ ImageService = (*imageService)(nil)

// imageService is the concrete implementation of the ImageService interface.
type imageService struct {
	sql       data.SqlRepository
	indexer   data.Indexer
	cryptor   crypt.Cryptor // image data specific wrapper around data.Cryptor
	store     storage.ObjectStorage
	reprocess chan pipeline.ReprocessCmd

	logger *slog.Logger
}

// GetImageData is the concrete implementation of the interface method which
// retrieves image data from the database (based on the user's permissions) and
// fetches signed URL for the image.
func (s *imageService) GetImageData(slug string, userPs map[string]permissions.PermissionRecord) (*api.ImageData, error) {

	// validate the slug
	// redundant check, but good practice
	if !validate.IsValidUuid(slug) {
		return nil, fmt.Errorf("image slug '%s' is not well-formed", slug)
	}

	// get blind index for the slug
	index, err := s.indexer.ObtainBlindIndex(slug)
	if err != nil {
		return nil, fmt.Errorf("failed to generate blind index for image slug '%s': %v", slug, err)
	}

	// build query based on the user's permissions
	qry := BuildGetImageQuery(userPs)

	// create the []args ...interface{} slice
	args := make([]interface{}, 0, len(userPs)+1)

	// add the slug index as the first argument
	args = append(args, index)

	// if the user is not a curator/admin, add the permission uuids as the remaining arguments
	if _, ok := userPs[util.PermissionCurator]; !ok {
		for _, p := range userPs {
			args = append(args, p.Id)
		}
	}

	var record api.ImageRecord
	if err := s.sql.SelectRecord(qry, &record, args...); err != nil {
		// all of the following presume the user is not a curator/admin.
		if err == sql.ErrNoRows {

			// check if the image exists at all
			if exists, err := s.sql.SelectExists(BuildImageExistsQry(), index); err != nil {
				return nil, fmt.Errorf("failed to check if image exists for slug '%s': %v", slug, err)
			} else if !exists {
				return nil, fmt.Errorf("image '%s' was not found", slug)
			}

			// check if the image exists but the user has not permissions
			if exists, err := s.sql.SelectExists(BuildImagePermissionsQry(userPs), args...); err != nil {
				return nil, fmt.Errorf("failed to check if image exists for slug '%s': %v", slug, err)
			} else if exists {
				return nil, fmt.Errorf("user does not have permission to view image '%s'", slug)
			}

			// check if the image is archived
			if exists, err := s.sql.SelectExists(BuildImageArchivedQry(), index); err != nil {
				return nil, fmt.Errorf("failed to check if image is archived for slug '%s': %v", slug, err)
			} else if exists {
				return nil, fmt.Errorf("image '%s' is archived", slug)
			}

			// check if the image is published
			if exists, err := s.sql.SelectExists(BuildImagePublishedQry(), index); err != nil {
				return nil, fmt.Errorf("failed to check if image is published for slug '%s': %v", slug, err)
			} else if exists {
				return nil, fmt.Errorf("image '%s' is not published", slug)
			}

			// unknown error
			return nil, fmt.Errorf("image '%s' was not found for unknown/unaccounted for reason", slug)
		}
		return nil, fmt.Errorf("failed to get image data for slug '%s': %v", slug, err)
	}

	// decrypt the sensitive fields in the image record
	if err := s.cryptor.DecryptImageRecord(&record); err != nil {
		return nil, fmt.Errorf("failed to decrypt image record for slug '%s': %v", slug, err)
	}

	// Generate a signed URL for the image from object storage service
	if record.ObjectKey == "" {
		return nil, fmt.Errorf("object key for image '%s' is empty", slug)
	}

	// get the directory of the image object key
	dir, _, ext, slug, err := pipeline.ParseObjectKey(record.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse object key for image '%s': %v", slug, err)
	}

	var (
		wg sync.WaitGroup

		urlsCh = make(chan api.ImageTarget, len(util.ResolutionWidthsImages)+1)
		blurCh = make(chan string, 1)

		errCh = make(chan error, len(util.ResolutionWidthsImages)+2)
	)

	// get the highest resolution signed URL
	wg.Add(1)
	go s.getObjectUrl(record.ObjectKey, record.Width, urlsCh, errCh, &wg)

	// get signed URLs for each resolution width
	for _, width := range util.ResolutionWidthsImages {
		// build the object key for the resized image
		resizedKey := fmt.Sprintf("%s/%s_w%d%s", dir, slug, width, ext)

		wg.Add(1)
		go s.getObjectUrl(resizedKey, width, urlsCh, errCh, &wg)
	}

	// get the signed URL for the blur placeholder image
	wg.Add(1)
	go func() {
		defer wg.Done()

		blurKey := fmt.Sprintf("%s/%s_blur%s", dir, slug, ext)

		url, err := s.store.GetSignedUrl(blurKey)
		if err != nil {
			errCh <- fmt.Errorf("failed to get signed URL for blur object key '%s': %v", blurKey, err)
			return
		}

		if url == nil || url.String() == "" {
			errCh <- fmt.Errorf("signed URL for blur object key '%s' is empty", blurKey)
			return
		}

		blurCh <- url.String()
	}()

	// wait for all goroutines to finish
	wg.Wait()
	close(urlsCh)
	close(blurCh)
	close(errCh)

	// check for errors
	if len(errCh) > 0 {
		errMsgs := make([]string, 0, len(errCh))
		for e := range errCh {
			errMsgs = append(errMsgs, e.Error())
		}
		return nil, fmt.Errorf("failed to get signed URLs for image '%s': %s", slug, strings.Join(errMsgs, "; "))
	}

	// collect the signed URLs
	signedURLs := make([]api.ImageTarget, 0, len(urlsCh))
	for url := range urlsCh {
		signedURLs = append(signedURLs, url)
	}

	if len(signedURLs) == 0 {
		return nil, fmt.Errorf("no signed URLs found for image '%s'", slug)
	}

	// create the ImageData struct to return
	image := &api.ImageData{
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

		ImageTargets: signedURLs,
		BlurUrl:      <-blurCh,
	}

	return image, nil
}

// BuildPlaceholder is the concrete implementation of the interface method which
// builds the metadata for a placeholder image from an add image cmd.
// The image processing pipeline will build the rest of the record upon ingestion of the image file.
func (s *imageService) BuildPlaceholder(cmd api.AddMetaDataCmd) (*api.Placeholder, error) {

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
	record := api.ImageRecord{
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
	data := &api.Placeholder{
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
func (s *imageService) UpdateImageData(ctx context.Context, existing *api.ImageData, updated *api.ImageRecord) error {

	// create function scoped logger
	// add telemetry fields from context if exists
	log := s.logger
	if tel, ok := connect.GetTelemetryFromContext(ctx); ok && tel != nil {
		log = log.With(tel.TelemetryFields()...)
	} else {
		log.Warn("no telemetry found in context for buildImageData")
	}

	// validate updated image record
	// redundant check, but good practice
	if err := updated.Validate(); err != nil {
		return fmt.Errorf("failed to validate updated image record: %v", err)
	}

	// is update necessary?
	// redundant check, but good practice
	if existing.Title == updated.Title &&
		existing.Description == updated.Description &&
		existing.ImageDate == updated.ImageDate &&
		existing.ObjectKey == updated.ObjectKey &&
		existing.IsArchived == updated.IsArchived &&
		existing.IsPublished == updated.IsPublished {
		log.Warn(fmt.Sprintf("no changes detected for image slug '%s', skipping update", existing.Slug))
		return nil
	}

	// get the blind index for the slug
	// using existing slug since this should not be updated generally..
	// if it needs to be updated, the slug should be specifically changed by a separate method.
	index, err := s.indexer.ObtainBlindIndex(existing.Slug)
	if err != nil {
		return fmt.Errorf("failed to generate blind index for image slug '%s': %v", updated.Slug, err)
	}

	// need to encrypt a copy of the updated image record
	encrypted := *updated

	// encrypt the sensitive fields in the updated image record
	if err := s.cryptor.EncryptImageRecord(&encrypted); err != nil {
		return fmt.Errorf("failed to encrypt updated image slug '%s' data: %v", updated.Slug, err)
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
		encrypted.Title,
		encrypted.Description,
		encrypted.ObjectKey,
		encrypted.ImageDate,
		encrypted.UpdatedAt,
		encrypted.IsArchived,
		encrypted.IsPublished,
		index); err != nil {
		return fmt.Errorf("failed to update image record id '%s' in database: %v", existing.Id, err)
	}

	// if the object key has changed, need to update the object storage service
	// by file naming convention, the only way these would not be equal is if
	// the dir (possibly 'staged', 'errored', or a different year) in the object key has changed
	if updated.ObjectKey != existing.ObjectKey {

		// dir -> year or staged/errored, etc.
		log.Warn(fmt.Sprintf("image slug %s's object key changed from '%s' to '%s', reprossessing...",
			updated.Slug, existing.ObjectKey, updated.ObjectKey))

		cmd := pipeline.ReprocessCmd{
			Id:            existing.Id,
			FileName:      updated.FileName,
			FileType:      updated.FileType,
			Slug:          existing.Slug,
			CurrentObjKey: existing.ObjectKey,
			UpdatedObjKey: updated.ObjectKey,
			MoveRequired:  true,
			RetryCount:    1, // first attempt
		}

		// send to reprocessing queue
		s.reprocess <- cmd
	}

	return nil
}

// getObjectUrl is a helper method which generates a signed URL for the provided object key
// from the object storage service and returns the URL as a string.
func (s *imageService) getObjectUrl(key string, width int, urlCh chan api.ImageTarget, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	url, err := s.store.GetSignedUrl(key)
	if err != nil {
		errCh <- fmt.Errorf("failed to get signed URL for object key '%s': %v", key, err)
		return
	}

	if url == nil || url.String() == "" {
		errCh <- fmt.Errorf("signed URL for object key '%s' is empty", key)
		return
	}

	urlCh <- api.ImageTarget{
		Width:     width,
		SignedUrl: url.String(),
	}
}
