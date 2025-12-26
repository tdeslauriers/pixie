package pipeline

import (
	"database/sql"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/crypt"
	"github.com/tdeslauriers/pixie/internal/util"

	"github.com/tdeslauriers/pixie/pkg/api"
)

// ImagePipline provides methods for processing image files submitted to the pipeline.
type ImagePipline interface {

	// UploadQueue processes images submitted to the pipeline queue, parsing the webhook,
	// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
	// directory in object storage, typically based on the image year date.
	UploadQueue()

	// ReprocessQueue reprocesses images in the pipeline queue, based on the ReprocessCmd instructions/criteria.
	// It is primarily used for reprocessing images that may have failed initial processing, such as images
	// that were uploaded without exif data and landed in staging.  It is also called in order to generate any
	// missing image resolutions or tile resolutions that errored upon initial processing.
	ReprocessQueue()
}

// NewImagePipeline creates a new instance of ImageProcessor, returning
// a pointer to the concrete implementation.
func NewImagePipeline(
	up chan storage.WebhookPutObject,
	re chan ReprocessCmd,
	wg *sync.WaitGroup,
	db *sql.DB,
	i data.Indexer,
	c data.Cryptor,
	o storage.ObjectStorage,
) ImagePipline {

	return &imagePipeline{
		uploadQueue:    up,
		reprocessQueue: re,
		wg:             wg,

		db:       NewRepository(db),
		indexer:  i,
		cryptor:  crypt.NewCryptor(c),
		objStore: o,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePipeline)).
			With(slog.String(util.ComponentKey, util.ComponentImageProcessor)),
	}
}

var _ ImagePipline = (*imagePipeline)(nil)

// imagePipeline is the concrete implementation of the ImageProcessor interface, which
// provides methods for processing image files submitted to the pipeline.
type imagePipeline struct {
	uploadQueue    chan storage.WebhookPutObject
	reprocessQueue chan ReprocessCmd
	wg             *sync.WaitGroup

	db       Repository
	indexer  data.Indexer
	cryptor  crypt.Cryptor
	objStore storage.ObjectStorage

	logger *slog.Logger
}

// ProcessImages is a concrete implementation of the interface method which
// processes images submitted to the pipeline queue, parsing the webhook,
// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
// directory in object storage, typically based on the image year date.
func (p *imagePipeline) UploadQueue() {

	defer p.wg.Done()

	for webhook := range p.uploadQueue {

		// generate telemetry -> in this case just a trace parent for web calls
		telemetry := &connect.Telemetry{
			Traceparent: *connect.GenerateTraceParent(),
		}

		log := p.logger.With(telemetry.TelemetryFields()...)

		// validate webhook
		// redundant check, but good practice
		if err := webhook.Validate(); err != nil {
			log.Error(fmt.Sprintf("invalid webhook received in image processing pipeline: %v", err))
			continue
		}

		log.Info(fmt.Sprintf("processing image upload notification for object %s in bucket %s", webhook.MinioKey, webhook.Records[0].S3.Bucket.Name))

		// parse and validate the object key to get the slug
		// the object key is in the format of "directory/{slug}.extension"
		dir, file, ext, slug, err := ParseObjectKey(webhook.MinioKey)
		if err != nil {
			log.Error(fmt.Sprintf("failed to parse object key %s from webhook: %v", webhook.MinioKey, err))
			continue
		}

		uploadKey := fmt.Sprintf("%s/%s", dir, file)

		// stream the image file from object storage
		// process the image (read exif, generate thumbnails, etc)
		// move the image to the correct directory in object storage based on the image date or current year
		// if no image date found in exif data
		if err := p.objStore.WithObject(uploadKey, func(r storage.ReadSeekCloser) error {

			// read the exif data if it exists
			// NOTE: some images may not have exif data, so checks for default/zero values are needed
			meta, err := ReadExif(r)
			if err != nil {
				return fmt.Errorf("failed to read exif data for object %s: %v", webhook.MinioKey, err)
			}

			//get the image record from the database
			img, err := p.getImageRecord(slug)
			if err != nil {
				return fmt.Errorf("failed to retrieve image record from database for image with slug %s: %v", slug, err)
			}

			// get the album records associated with the image
			albums, err := p.getImageAlbums(img.Id)
			if err != nil {
				return fmt.Errorf("failed to retrieve album records from database for image with id %s: %v", img.Id, err)
			}

			// parse the image date from exif data if it exists to get the album year
			// if exif data is found, update the ImageDate, the ObjectKey, and link to the year album
			if meta.TakenAt != nil {

				year := strconv.Itoa(meta.TakenAt.Year())

				// check if an album for the year already associated with image
				if _, ok := albums[year]; !ok {
					// if not, create a new album for the year
					if err := p.linkToAlbum(year, img); err != nil {
						return fmt.Errorf("failed to create album for year %s for image with id %s: %v", year, img.Id, err)
					}
				}

				// update/set the image ImageDate field
				img.ImageDate = meta.TakenAt.UTC().Format(time.RFC3339)

				// set the directory to the year from the image date -> ObjectKey
				dir = year
			} else {

				// set the directory to 'staging' if no exif date found - ObjectKey
				dir = "staging"
			}

			// update the ObjectKey to the new directory
			img.ObjectKey = fmt.Sprintf("%s/%s%s", dir, slug, ext)

			// if the exif data contains width, update
			if meta.Width != 0 {
				img.Width = meta.Width
			}

			// if the exif data contains height, update
			if meta.Height != 0 {
				img.Height = meta.Height
			}

			// TODO add gps coordinates to image data

			// generate src set of different image resolutions + blur/placeholder
			src, _, err := image.Decode(r)
			if err != nil {
				return fmt.Errorf("failed to image-format-decode (jpeg/png) image for object %s: %v", img.ObjectKey, err)
			}

			// apply orientation if needed -> default is zero, so dont need to check if exif existed.
			src = rotateImage(src, meta.Rotation)

			// concurrently generate and upload the different resolution images, tiles, and blur/placeholder
			var (
				wg    sync.WaitGroup
				errCh = make(chan error, len(util.ResolutionWidthsImages)+len(util.ResolutionWidthsTiles)+2)
			)

			// generate and upload the different resolution images for src set
			for _, width := range util.ResolutionWidthsImages {

				wg.Add(1)
				go func(w int, ch chan error, wg *sync.WaitGroup) {

					defer wg.Done()

					// resize the image to the target width, maintaining aspect ratio
					// encode to jpeg, and upload to object storage
					resizedKey := fmt.Sprintf("%s/%s_w%d%s", filepath.Dir(img.ObjectKey), slug, width, ext)
					if err := p.resizeAndPut(src, w, resizedKey, img.FileType); err != nil {
						ch <- fmt.Errorf("failed to upload resized resolution image %s to object storage: %v", resizedKey, err)
					}

					log.Info(fmt.Sprintf("upload processing successfully processed resized image %s with width %d", resizedKey, w))

				}(width, errCh, &wg)
			}

			// generate the tiles
			for _, width := range util.ResolutionWidthsTiles {

				wg.Add(1)
				go func(w int, ch chan error, wg *sync.WaitGroup) {

					defer wg.Done()

					// resize the image to the target width, maintaining aspect ratio
					// encode to jpeg, and upload to object storage
					tileKey := fmt.Sprintf("%s/%s_tile_w%d%s", filepath.Dir(img.ObjectKey), slug, width, ext)
					if err := p.resizeAndPut(src, w, tileKey, img.FileType); err != nil {
						ch <- fmt.Errorf("failed to upload tile image %s to object storage: %v", tileKey, err)
					}

					log.Info(fmt.Sprintf("upload processing successfully processed tile image %s for width %d", tileKey, w))

				}(width, errCh, &wg)
			}

			// generate and upload blur/placeholder image (hard downscale -> soft blur)
			wg.Add(1)
			go func(ch chan error, wg *sync.WaitGroup) {

				defer wg.Done()

				blur := resizeToLongestSide(src)
				encoded, err := encodeToJpeg(blur, JpegQuality)
				if err != nil {
					ch <- fmt.Errorf("failed to encode blur/placeholder image to jpeg for uploaded object %s: %v", img.ObjectKey, err)
				}

				// upload the blur/placeholder image to object storage in the same directory as the original image
				blurKey := fmt.Sprintf("%s/%s_blur%s", filepath.Dir(img.ObjectKey), slug, ext)
				if err := p.objStore.PutObject(blurKey, encoded, img.FileType); err != nil {
					ch <- fmt.Errorf("failed to upload blur/placeholder image %s to object storage: %v", blurKey, err)
				}

				log.Info(fmt.Sprintf("upload processing successfully processed blur/placeholder image %s", blurKey))
			}(errCh, &wg)

			// move the image to the correct directory in object storage
			wg.Add(1)
			go func(ch chan error, wg *sync.WaitGroup) {
				defer wg.Done()

				if err := p.objStore.MoveObject(uploadKey, img.ObjectKey); err != nil {
					ch <- fmt.Errorf("failed to move uploaded object %s to new location %s in object storage: %v", webhook.MinioKey, img.ObjectKey, err)
				}

				log.Info(fmt.Sprintf("successfully moved uploaded object %s to new location %s in object storage", uploadKey, img.ObjectKey))
			}(errCh, &wg)

			// wait for all goroutines to finish
			wg.Wait()
			close(errCh)

			// check for errors from goroutines
			if len(errCh) > 0 {
				errs := make([]error, len(errCh))
				for err := range errCh {
					errs = append(errs, err)
				}
				return fmt.Errorf("one or more errors occurred during image processing for object %s: %v", webhook.MinioKey, errors.Join(errs...))
			}

			// check if directroy is a year  or if it is 'staging' and set is_published flag accordingly
			if dir != "staging" {
				img.IsPublished = true
			} else {
				img.IsPublished = false
			}

			// update the image record in the database
			// Note: includes re-encrypting the record fields
			if err := p.updateImageRecord(img); err != nil {
				return err
			}

			log.Info(fmt.Sprintf("successfully processed image with slug %s", slug))

			return nil
		}); err != nil {
			log.Error(fmt.Sprintf("failed to process image %s: %v", uploadKey, err))
			continue
		}
	}
}

const MaxReprocessRetries int = 5

// ReprocessQueue is a concrete implementation of the interface method which
// reprocesses images in the pipeline queue, based on the ReprocessCmd instructions/criteria.
// It is primarily used for reprocessing images that may have failed initial processing, such as images
// that were uploaded without exif data and landed in staging.  It is also called in order to generate any
// missing image resolutions or tile resolutions that errored upon initial processing.
func (p *imagePipeline) ReprocessQueue() {

	defer p.wg.Done()

	for cmd := range p.reprocessQueue {

		// generate telemetry -> in this case just a trace parent for web calls
		telemetry := &connect.Telemetry{
			Traceparent: *connect.GenerateTraceParent(),
		}
		log := p.logger.With(telemetry.TelemetryFields()...)

		if cmd.RetryCount < MaxReprocessRetries {

			// field validation omitted because this command is internal only, never takes user input

			// check whether a file move is required
			// note: current state: a move is always required but this may change in the future
			if cmd.MoveRequired {

				// for now, mvp is just to move the file and fix/add any missing resolutions/tiles
				log.Info(fmt.Sprintf("reprocessing image with slug %s: attempt %d", cmd.Slug, cmd.RetryCount))

				// parse the existing/previous key so the resolution files names can be derived
				dir, _, ext, slug, err := ParseObjectKey(cmd.CurrentObjKey)
				if err != nil {
					log.Error(fmt.Sprintf("failed to parse existing object key %s for reprocess command (attempt %d): %v", cmd.CurrentObjKey, cmd.RetryCount, err))
					cmd.RetryCount++ // increment retry count
					// TODO: re-queue the command for retry if under max retries
					continue
				}

				// move the original object to the new location
				// this must be done first before moving/building resolutions/tiles in order
				// to validate the object exists in the first place.
				// if it does not exists, or fails to move, there is no point in continuing.
				if err := p.objStore.MoveObject(cmd.CurrentObjKey, cmd.UpdatedObjKey); err != nil {

					if strings.Contains(err.Error(), "does not exist in object storage") {
						log.Error(fmt.Sprintf("%s not found in object storage for reprocess command (attempt %d)", cmd.CurrentObjKey, cmd.RetryCount),
							"err", err.Error())
						cmd.RetryCount++ // increment retry count
						// TODO: re-queue the command for retry if under max retries
						continue
					} else {
						log.Error(fmt.Sprintf("failed to move %s to new location %s for reprocess command (attempt %d)", cmd.CurrentObjKey, cmd.UpdatedObjKey, cmd.RetryCount),
							"err", err.Error())
						cmd.RetryCount++ // increment retry count
						// TODO: re-queue the command for retry if under max retries
						continue
					}
				}

				log.Info(fmt.Sprintf("successfully moved %s to new location %s for reprocess command (attempt %d)", cmd.CurrentObjKey, cmd.UpdatedObjKey, cmd.RetryCount))

				// concurrently move original image and blur and/or (re)build missing resolutions/tiles
				var (
					wg    sync.WaitGroup
					errCh = make(chan error, len(util.ResolutionWidthsImages)+len(util.ResolutionWidthsTiles)+1)
				)

				// loop thru resolution widths: move existing and build missing
				for _, width := range util.ResolutionWidthsImages {

					wg.Add(1)
					go func(c *ReprocessCmd, w int, ch chan error, wg *sync.WaitGroup) {

						defer wg.Done()

						// derive existing/updated resolution object key -> based on file naming convention in object storage
						existingResKey := fmt.Sprintf("%s/%s_w%d%s", dir, slug, width, ext)
						updatedResKey := fmt.Sprintf("%s/%s_w%d%s", filepath.Dir(cmd.UpdatedObjKey), slug, width, ext)

						// the object should already exist, try to move it
						if err := p.objStore.MoveObject(existingResKey, updatedResKey); err != nil {

							// if it does not exist, need to build it
							if strings.Contains(err.Error(), "does not exist in object storage") {

								log.Warn(fmt.Sprintf("resolution image %s not found in object storage, (re)building for reprocess command (attempt %d)", existingResKey, c.RetryCount))

								// stream + create resolution the original image from object storage
								if err := p.objStore.WithObject(c.UpdatedObjKey, func(r storage.ReadSeekCloser) error {

									// decode the image
									src, _, err := image.Decode(r)
									if err != nil {
										return fmt.Errorf("failed to image-format-decode (jpeg/png) image for object %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
									}

									// resize the image to the target width, maintaining aspect ratio,
									// encode to jpeg, and upload to object storage
									if err := p.resizeAndPut(src, w, updatedResKey, c.FileType); err != nil {
										return fmt.Errorf("failed to upload resized resolution image %s to object storage for reprocess command (attempt %d): %v", updatedResKey, c.RetryCount, err)
									}

									return nil
								}); err != nil {
									ch <- fmt.Errorf("failed to (re)build resolution image %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
									return
								}
							} else {
								ch <- fmt.Errorf("failed to move resolution image %s to new location %s for reprocess command (attempt %d): %v", existingResKey, updatedResKey, c.RetryCount, err)
								return
							}
						}

						// successfully moved existing resolution image
						log.Info(fmt.Sprintf("successfully moved resolution image %s to new location %s for reprocess command (attempt %d)", existingResKey, updatedResKey, c.RetryCount))
					}(&cmd, width, errCh, &wg)

				}

				// loop thru tile widths: move existing and build missing
				for _, width := range util.ResolutionWidthsTiles {

					wg.Add(1)
					go func(c *ReprocessCmd, w int, ch chan error, wg *sync.WaitGroup) {

						defer wg.Done()

						// derive existing/updated tile object key -> based on file naming convention in object storage
						existingTileKey := fmt.Sprintf("%s/%s_tile_w%d%s", dir, slug, width, ext)
						updatedTileKey := fmt.Sprintf("%s/%s_tile_w%d%s", filepath.Dir(c.UpdatedObjKey), slug, width, ext)

						// the object should already exist, try to move it
						if err := p.objStore.MoveObject(existingTileKey, updatedTileKey); err != nil {

							// if it does not exist, need to build it
							if strings.Contains(err.Error(), "does not exist in object storage") {

								log.Warn(fmt.Sprintf("tile image %s not found in object storage, (re)building for reprocess command (attempt %d)", existingTileKey, c.RetryCount))

								// stream + create tile the original image from object storage
								if err := p.objStore.WithObject(c.UpdatedObjKey, func(r storage.ReadSeekCloser) error {

									// decode the image
									src, _, err := image.Decode(r)
									if err != nil {
										return fmt.Errorf("failed to image-format-decode (jpeg/png) image for object %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
									}

									// resize the image to the target width, maintaining aspect ratio,
									// encode to jpeg, and upload to object storage
									if err := p.resizeAndPut(src, width, updatedTileKey, c.FileType); err != nil {
										return fmt.Errorf("failed to upload resized tile image %s to object storage for reprocess command (attempt %d): %v", updatedTileKey, c.RetryCount, err)
									}

									return nil
								}); err != nil {
									ch <- fmt.Errorf("failed to (re)build tile image %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
									return
								}
							} else {
								ch <- fmt.Errorf("failed to move tile image %s to new location %s for reprocess command (attempt %d): %v", existingTileKey, updatedTileKey, c.RetryCount, err)
								return
							}
						}

						// successfully moved existing tile image
						log.Info(fmt.Sprintf("successfully moved tile image %s to new location %s for reprocess command (attempt %d)", existingTileKey, updatedTileKey, c.RetryCount))
					}(&cmd, width, errCh, &wg)
				}

				// move the blur/placeholder image to the new location or (re)build if missing
				wg.Add(1)
				go func(c *ReprocessCmd, ch chan error, wg *sync.WaitGroup) {

					defer wg.Done()

					// derive existing/updated blur object key -> based on file naming convention in object storage
					existingBlurKey := fmt.Sprintf("%s/%s_blur%s", dir, slug, ext)
					updatedBlurKey := fmt.Sprintf("%s/%s_blur%s", filepath.Dir(c.UpdatedObjKey), slug, ext)

					// the object should already exist, try to move it
					if err := p.objStore.MoveObject(existingBlurKey, updatedBlurKey); err != nil {

						// if it does not exist, need to build it
						if strings.Contains(err.Error(), "does not exist in object storage") {

							log.Warn(fmt.Sprintf("blur/placeholder image %s not found in object storage, (re)building for reprocess command (attempt %d)", existingBlurKey, c.RetryCount))

							// stream + create blur/placeholder the original image from object storage
							if err := p.objStore.WithObject(c.UpdatedObjKey, func(r storage.ReadSeekCloser) error {

								// decode the image
								src, _, err := image.Decode(r)
								if err != nil {
									return fmt.Errorf("failed to image-format-decode (jpeg/png) image for object %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
								}

								// generate blur/placeholder image
								blur := resizeToLongestSide(src)
								encoded, err := encodeToJpeg(blur, JpegQuality)
								if err != nil {
									return fmt.Errorf("failed to encode blur/placeholder image to jpeg for uploaded object %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
								}

								// upload the blur/placeholder image to object storage in the same directory as the original image
								if err := p.objStore.PutObject(updatedBlurKey, encoded, c.FileType); err != nil {
									return fmt.Errorf("failed to upload blur/placeholder image %s to object storage for reprocess command (attempt %d): %v", updatedBlurKey, c.RetryCount, err)
								}

								return nil
							}); err != nil {
								ch <- fmt.Errorf("failed to (re)build blur/placeholder image %s for reprocess command (attempt %d): %v", c.UpdatedObjKey, c.RetryCount, err)
								return
							}
						} else {
							ch <- fmt.Errorf("failed to move blur/placeholder image %s to new location %s for reprocess command (attempt %d): %v", existingBlurKey, updatedBlurKey, c.RetryCount, err)
							return
						}
					}

					// successfully moved existing blur/placeholder image
					log.Info(fmt.Sprintf("successfully moved blur/placeholder image %s to new location %s for reprocess command (attempt %d)", existingBlurKey, updatedBlurKey, c.RetryCount))
				}(&cmd, errCh, &wg)

				// wait for all goroutines to finish
				wg.Wait()
				close(errCh)

				// check for errors from goroutines
				if len(errCh) > 0 {
					errs := make([]error, len(errCh))
					for err := range errCh {
						errs = append(errs, err)
					}
					log.Error(fmt.Sprintf("one or more errors occurred during reprocessing image with slug %s for reprocess command (attempt %d): %v", cmd.Slug, cmd.RetryCount, errors.Join(errs...)))
					cmd.RetryCount++ // increment retry count
					// TODO: re-queue the command for retry if under max retries
					continue
				}

				log.Info(fmt.Sprintf("successfully reprocessed image with slug %s for reprocess command (attempt %d)", cmd.Slug, cmd.RetryCount))

				// ensure that there is a an album associated with the year if applicable
				// Note: if made it this far, that should mean the move(s) was successful
				// parse the updated object key to get the directory/year
				year, _, _, _, err := ParseObjectKey(cmd.UpdatedObjKey)
				if err != nil {
					log.Error(fmt.Sprintf("failed to parse updated object key %s for reprocess command (attempt %d): %v", cmd.UpdatedObjKey, cmd.RetryCount, err))
					cmd.RetryCount++ // increment retry count
					// TODO: re-queue the command for retry if under max retries
					continue
				}

				// by naming convention, the directory of a published image should be the year,
				// so it should parse to a number
				if _, err := strconv.Atoi(year); err != nil {
					// get the image record from the database
					log.Error(fmt.Sprintf("directory %s parsed from updated object key %s is not a valid year for reprocess command (attempt %d): %v", year, cmd.UpdatedObjKey, cmd.RetryCount, err))
					cmd.RetryCount++ // increment retry count
					// TODO: re-queue the command for retry if under max retries
					continue
				}

				// create xref
				if err := p.linkToAlbum(year, &api.ImageRecord{Id: cmd.Id}); err != nil {
					log.Error(fmt.Sprintf("failed to create album xref for year %s for image with id %s for reprocess command (attempt %d): %v", year, cmd.Id, cmd.RetryCount, err))
					cmd.RetryCount++ // increment retry count
					// TODO: re-queue the command for retry if under max retries
					continue
				}
			}
		} else {
			log.Error(fmt.Sprintf("max retries reached for reprocess command for image with slug %s, skipping further attempts", cmd.Slug))
		}
	}
}

// putResizedImage is a helper which resizes the provided image to the target width,
// encodes it to jpeg, and uploads it to object storage at the specified key.
// Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) resizeAndPut(src image.Image, targetWidth int, objKey string, fileType string) error {

	// resize the image to the target width, maintaining aspect ratio
	resized := resizeImageToWidth(src, targetWidth)
	encoded, err := encodeToJpeg(resized, JpegQuality)
	if err != nil {
		return fmt.Errorf("failed to encode resized image to jpeg for object %s: %v", objKey, err)
	}

	// upload the resized image to object storage at the specified key
	if err := p.objStore.PutObject(objKey, encoded, fileType); err != nil {
		return fmt.Errorf("failed to upload resized image %s to object storage: %v", objKey, err)
	}

	return nil
}

// getImageRecord is a help retrieves the image record from the database using the provided object key.
// Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) getImageRecord(slug string) (*api.ImageRecord, error) {

	// validate slug
	// redundant check, but good practice
	if !validate.IsValidUuid(slug) {
		return nil, fmt.Errorf("invalid image slug: %s", slug)
	}

	// get the slug index for record lookup
	index, err := p.indexer.ObtainBlindIndex(slug)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain slug index for image with slug %s: %v", slug, err)
	}

	// query the database for the image record by its slug index
	record, err := p.db.FindImage(index)
	if err != nil {
		return nil, fmt.Errorf("failed to query image record for slug %s: %w", slug, err)
	}

	// decrypt the image record fields
	if err := p.cryptor.DecryptImageRecord(record); err != nil {
		return nil, fmt.Errorf("failed to decrypt image record for slug %s: %v", slug, err)
	}

	return record, nil
}

// updateImageRecord is a helper which updates the image record in the database based on
// the exif data if present. Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) updateImageRecord(img *api.ImageRecord) error {

	// validate image record
	if img == nil {
		return fmt.Errorf("image record is nil")
	}

	// encrypt the image record fields before updating
	if err := p.cryptor.EncryptImageRecord(img); err != nil {
		return fmt.Errorf("failed to encrypt image record for image with id %s: %v", img.Id, err)
	}

	// update the image record in the database
	if err := p.db.UpdateImage(*img); err != nil {
		return fmt.Errorf("failed to update image record in database for image with id %s: %v", img.Id, err)
	}

	return nil
}

// getImageAlbums is a helper which retrieves the album records associated with the image
// using the provided image UUID. Exists to abstract away this logic from the main processing loop
// for readability.  It returns a map for easy lookups of album titles.
func (p *imagePipeline) getImageAlbums(imageUuid string) (map[string]struct{}, error) {

	// get albums associated with the image from database, if any -> possible none associated yet
	albums, err := p.db.FindImageAlbums(imageUuid)
	if err != nil {
		return nil, fmt.Errorf("failed to query albums for image id %s: %v", imageUuid, err)
	}

	// if albums found, decrypt them and make a quick lookup map
	albumsMap := make(map[string]struct{}) // unlikely to be greater than 16 so no need to preallocate
	if len(albums) > 0 {
		for _, album := range albums {
			if err := p.cryptor.DecryptAlbumRecord(&album); err != nil {
				return nil, fmt.Errorf("failed to decrypt album record for album with id %s: %v", album.Id, err)
			}

			// key is title field because primarily looking for albums of year like '2023', '2022', etc.
			albumsMap[album.Title] = struct{}{}
		}
	}

	return albumsMap, nil
}

// linkToAlbum is a helper which links the image to an existing album by title,
// or creates a new album if one does not exist. Exists to abstract away this logic
// from the main processing loop for readability.
func (p *imagePipeline) linkToAlbum(title string, img *api.ImageRecord) error {

	// validate inputs
	if title == "" {
		return fmt.Errorf("album title is empty")
	}

	if !validate.MatchesRegex(strings.TrimSpace(title), api.AlbumTitleRegex) {
		return fmt.Errorf("invalid album title: %s", title)
	}

	if img == nil {
		return fmt.Errorf("image record is nil")
	}

	if !validate.IsValidUuid(img.Id) {
		return fmt.Errorf("invalid image Id: %s", img.Id)
	}

	// get album records from database
	albums, err := p.db.FindAllAlbums()
	if err != nil {
		return fmt.Errorf("failed to query all album records: %v", err)
	}

	albumMap := make(map[string]api.AlbumRecord, len(albums))
	for _, a := range albums {
		if err := p.cryptor.DecryptAlbumRecord(&a); err != nil {
			return fmt.Errorf("failed to decrypt album record for album with id %s: %v", a.Id, err)
		}

		albumMap[a.Title] = a
	}

	// check if album with title already exists
	var albumId string
	if a, ok := albumMap[title]; ok {
		albumId = a.Id
	} else {
		// create a new album record
		id, err := uuid.NewRandom()
		if err != nil {
			return fmt.Errorf("failed to generate UUID for new album with title %s: %v", title, err)
		}

		slug, err := uuid.NewRandom()
		if err != nil {
			return fmt.Errorf("failed to generate slug UUID for new album with title %s: %v", title, err)
		}

		// generate slug index
		slugIndex, err := p.indexer.ObtainBlindIndex(slug.String())
		if err != nil {
			return fmt.Errorf("failed to obtain slug index for new album with title %s: %v", title, err)
		}

		// this will always be a year album, so description is standardized
		newAlbum := api.AlbumRecord{
			Id:          id.String(),
			Title:       title,
			Description: fmt.Sprintf("Auto-generated album for year %s", title),
			Slug:        slug.String(),
			SlugIndex:   slugIndex,
			CreatedAt:   data.CustomTime{Time: time.Now().UTC()},
			UpdatedAt:   data.CustomTime{Time: time.Now().UTC()},
			IsArchived:  false,
		}

		// encrypt the album record fields before inserting
		if err := p.cryptor.EncryptAlbumRecord(&newAlbum); err != nil {
			return fmt.Errorf("failed to encrypt new album record for album with title %s: %v", title, err)
		}

		// insert the new album record into the database
		if err := p.db.InsertAlbum(newAlbum); err != nil {
			return fmt.Errorf("failed to insert new album record for album with title %s: %v", title, err)
		}

		// set the albumId to the new album's uuid
		albumId = newAlbum.Id
	}

	// link the image to the album in the album_image xref table
	xref := api.AlbumImageXref{
		Id:        0,
		AlbumId:   albumId,
		ImageId:   img.Id,
		CreatedAt: data.CustomTime{Time: time.Now().UTC()},
	}
	if err := p.db.InsertAlbumImageXref(xref); err != nil {
		return fmt.Errorf("failed to link image with id %s to album with id %s: %v", img.Id, albumId, err)
	}

	return nil
}
