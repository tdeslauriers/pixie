package picture

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
	"github.com/tdeslauriers/pixie/pkg/crypt"
	"github.com/tdeslauriers/pixie/pkg/pipeline"
)

// Staged album data.
// The rest of the fields are intentionally omitted.
const (
	StagedAlbumTitle       = "Staged Images"
	StagedAlbumDescription = "This album contains images that have been uploaded and landed in the staging area because they did not complete processing in the pipeline or failed for some reason.  It is possible they just need to be deleted."
)

// StagedImageService is a service for managing staged images.
type StagedImageService interface {

	// GetStagedImages retrieves the staged images album.
	// Note: it assumes the calling function has already verified the requester's permissions.
	GetStagedImages(ctx context.Context) (*api.Album, error)
}

// NewStagedImageService creates a new StagedImageService.
func NewStagedImageService(sql data.SqlRepository, i data.Indexer, c data.Cryptor, obj storage.ObjectStorage) StagedImageService {
	return &stagedImageService{
		sql:      sql,
		indexer:  i,
		cryptor:  crypt.NewCryptor(c),
		objStore: obj,

		logger: slog.Default().
			With(slog.String(util.ComponentKey, util.ComponentStagedImageService)).
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ StagedImageService = (*stagedImageService)(nil)

// stagedImageService is the default concrete implementation of StagedImageService.
type stagedImageService struct {
	sql      data.SqlRepository
	indexer  data.Indexer
	cryptor  crypt.Cryptor
	objStore storage.ObjectStorage

	logger *slog.Logger
}

// GetStagedImages is the concrete implementation of the StagedImagesService interface method which
// retrieves the staged images album.
// Note: it assumes the calling function has already verified the requester's permissions.
func (s *stagedImageService) GetStagedImages(ctx context.Context) (*api.Album, error) {

	// create function scoped logger
	// add telemetry fields from context if exists
	log := s.logger
	if tel, ok := connect.GetTelemetryFromContext(ctx); ok && tel != nil {
		log = log.With(tel.TelemetryFields()...)
	} else {
		log.Warn("no telemetry found in context for GetStagedImages")
	}

	// create aritficial "staged" album
	// the rest of the album fields are intentionally omitted
	album := &api.Album{
		Title:       StagedAlbumTitle,
		Description: StagedAlbumDescription,
	}

	// get all unpublished images metadata
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
	var images []db.ImageRecord
	if err := s.sql.SelectRecords(qry, &images); err != nil {
		return nil, fmt.Errorf("failed to retrieve unpublished images from database when retrieving staged images: %v", err)
	}

	if len(images) == 0 {
		s.logger.Info("no unpublished images found when retrieving staged images")
		return album, nil // no staged images
	}

	// decrypt image metadata
	var (
		decryptWg    sync.WaitGroup
		decryptErrCh = make(chan error, len(images))
	)

	for i, img := range images {
		decryptWg.Add(1)
		go func(i int, img db.ImageRecord) {
			defer decryptWg.Done()

			if err := s.cryptor.DecryptImageRecord(&img); err != nil {
				decryptErrCh <- fmt.Errorf("failed to decrypt metadata for staged image %s: %v", img.Id, err)
				return
			}

			images[i] = img
		}(i, img)
	}

	decryptWg.Wait()
	close(decryptErrCh)

	// check for decryption errors
	if len(decryptErrCh) > 0 {
		errs := make([]error, 0, len(decryptErrCh))
		for err := range decryptErrCh {
			errs = append(errs, err)
		}
		return nil, fmt.Errorf("one or more errors occurred while decrypting staged image metadata: %v", errors.Join(errs...))
	}

	// check the "directory" for each unpublished image to see if it is in staging"directory"
	// in object storage, e.g. "staging/dc1700b8-c45f-4811-9272-b7898ae84b03.jpg"
	var (
		stagedImgWg  sync.WaitGroup
		stagedImgCh  = make(chan api.ImageData, len(images))
		stagedErrCh  = make(chan error, len(images))
		stagedImages = make([]api.ImageData, 0, len(images))
	)
	for _, img := range images {

		stagedImgWg.Add(1)
		go func(ir db.ImageRecord, imgCh chan<- api.ImageData, errCh chan<- error, wg *sync.WaitGroup) {
			defer wg.Done()

			// get the "directory" part of the object key
			dir, _, ext, _, err := pipeline.ParseObjectKey(img.ObjectKey)
			if err != nil {
				stagedErrCh <- fmt.Errorf("failed to parse object key '%s' for unpublished image %s: %v",
					img.ObjectKey, img.Id, err)
				return
			}

			if dir == "" {
				stagedErrCh <- fmt.Errorf("invalid object key '%s' for unpublished image %s", img.ObjectKey, img.Id)
				return
			}

			// remove leading and trailing slash from directory
			dir = strings.Replace(dir, "/", "", -1)
			if dir != "staging" {
				// not in staging directory, unpublished for some other reason
				log.Warn(fmt.Sprintf("skipping unpublished image %s: not in staging directory", img.Id))
				return
			}

			// build the ImageData struct
			imageData := api.ImageData{
				Id:          ir.Id,
				Title:       ir.Title,
				Description: ir.Description,
				FileName:    ir.FileName,
				FileType:    ir.FileType,
				ObjectKey:   ir.ObjectKey,
				Slug:        ir.Slug,
				Width:       ir.Width,
				Height:      ir.Height,
				Size:        ir.Size,
				ImageDate:   ir.ImageDate,
				CreatedAt:   ir.CreatedAt.Format(time.RFC3339),
				UpdatedAt:   ir.UpdatedAt.Format(time.RFC3339),
				IsArchived:  ir.IsArchived,
				IsPublished: ir.IsPublished,
			}

			var (
				tileWg sync.WaitGroup

				tileCh = make(chan api.ImageTarget, len(util.ResolutionWidthsTiles))
				blurCh = make(chan string, 1)
				// no error channel since it is very probable resolutions are missing
			)

			// get the signed URLs for each resolution for tiles
			for _, width := range util.ResolutionWidthsTiles {

				tileKey := fmt.Sprintf("%s/%s_tile_w%d%s", dir, ir.Slug, width, ext)

				tileWg.Add(1)
				go s.getStagedObjectUrl(tileKey, width, tileCh, &tileWg)
			}

			// get the blur key signed URL
			blurKey := fmt.Sprintf("%s/%s_blur%s", dir, ir.Slug, ext)
			tileWg.Add(1)
			go func(key string, ch chan<- string, wg *sync.WaitGroup) {
				defer wg.Done()

				url, err := s.objStore.GetSignedUrl(key)
				if err != nil {
					log.Warn(fmt.Sprintf("failed to get signed URL for blur object key '%s': %v", key, err))
					return
				}

				if url == nil || url.String() == "" {
					log.Warn(fmt.Sprintf("signed URL for blur object key '%s' is empty", key))
					return
				}
				ch <- url.String()
			}(blurKey, blurCh, &tileWg)

			tileWg.Wait()
			close(tileCh)
			close(blurCh)

			// collect the tiles
			tiles := make([]api.ImageTarget, 0, len(tileCh))
			for t := range tileCh {
				tiles = append(tiles, t)
			}

			// set the imagedata tile imagetargets and blur
			imageData.ImageTargets = tiles
			imageData.BlurUrl = <-blurCh

			imgCh <- imageData

		}(img, stagedImgCh, stagedErrCh, &stagedImgWg)
	}

	stagedImgWg.Wait()
	close(stagedImgCh)
	close(stagedErrCh)

	// check for errors
	if len(stagedErrCh) > 0 {
		errs := make([]error, 0, len(stagedErrCh))
		for err := range stagedErrCh {
			errs = append(errs, err)
		}
		return nil, fmt.Errorf("error(s) occurred while retrieving staged images: %v", errors.Join(errs...))
	}

	// collect the staged images
	for img := range stagedImgCh {
		stagedImages = append(stagedImages, img)
	}

	// set the album images
	if len(stagedImages) > 0 {
		album.Images = stagedImages
	}

	return album, nil
}

// getStagedObjectUrl is a helper method which generates a signed URL for the provided object key
// from the object storage service and returns the URL as a string.
// NOTE: it is possible the resolution does not exists since we dont know when the pipeline
// errored and tossed the image in staged.
// As such, we just log missing things.
func (s *stagedImageService) getStagedObjectUrl(key string, width int, imgCh chan api.ImageTarget, wg *sync.WaitGroup) {

	defer wg.Done()

	// very possible a url cannot be generated for a missing object
	url, err := s.objStore.GetSignedUrl(key)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("failed to get signed URL for object key '%s': %v", key, err))
		return
	}

	if url == nil || url.String() == "" {
		s.logger.Warn(fmt.Sprintf("signed URL for object key '%s' is empty", key))
		return
	}

	imgCh <- api.ImageTarget{
		Width:     width,
		SignedUrl: url.String(),
	}
}
