package pipeline

import (
	"context"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/connect/telemetry"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"

	"github.com/tdeslauriers/pixie/pkg/api"
)

// ProcessImages is a concrete implementation of the interface method which
// processes images submitted to the pipeline queue, parsing the webhook,
// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
// directory in object storage, typically based on the image year date.
func (p *imagePipeline) UploadQueue(ctx context.Context) {

	defer p.wg.Done()

	for {
		// block until a webhook is received, the channel is closed, or the context is cancelled
		var webhook storage.WebhookPutObject
		select {
		case <-ctx.Done():
			return
		case w, ok := <-p.uploadQueue:
			if !ok {
				return
			}
			webhook = w
		}

		// child context with timeout for processing each image in the pipeline, to prevent hanging
		itemCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)

		// generate telemetry -> in this case just a trace parent for web calls
		telemetry := &telemetry.Telemetry{
			Traceparent: *telemetry.NewTraceparent(),
		}

		log := p.logger.With(telemetry.TelemetryFields()...)

		// validate webhook
		// redundant check, but good practice
		if err := webhook.Validate(); err != nil {
			log.Error(fmt.Sprintf("invalid webhook received in image processing pipeline: %v", err))
			cancel()
			continue
		}

		log.Info(fmt.Sprintf("processing image upload notification for object %s in bucket %s", webhook.MinioKey, webhook.Records[0].S3.Bucket.Name))

		// parse and validate the object key to get the slug
		// the object key is in the format of "directory/{slug}.extension"
		dir, file, ext, slug, err := ParseObjectKey(webhook.MinioKey)
		if err != nil {
			log.Error(fmt.Sprintf("failed to parse object key %s from webhook: %v", webhook.MinioKey, err))
			cancel()
			continue
		}

		uploadKey := fmt.Sprintf("%s/%s", dir, file)

		// stream the image file from object storage
		// process the image (read exif, generate thumbnails, etc)
		// move the image to the correct directory in object storage based on the image date or current year
		// if no image date found in exif data
		if err := p.objStore.WithObject(itemCtx, uploadKey, func(r storage.ReadSeekCloser) error {

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
					if err := p.resizeAndPut(itemCtx, src, w, resizedKey, img.FileType); err != nil {
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
					if err := p.resizeAndPut(itemCtx, src, w, tileKey, img.FileType); err != nil {
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
				if err := p.objStore.PutObject(itemCtx, blurKey, encoded, img.FileType); err != nil {
					ch <- fmt.Errorf("failed to upload blur/placeholder image %s to object storage: %v", blurKey, err)
				}

				log.Info(fmt.Sprintf("upload processing successfully processed blur/placeholder image %s", blurKey))
			}(errCh, &wg)

			// move the image to the correct directory in object storage
			wg.Add(1)
			go func(ch chan error, wg *sync.WaitGroup) {
				defer wg.Done()

				if err := p.objStore.MoveObject(itemCtx, uploadKey, img.ObjectKey); err != nil {
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
			cancel()
			continue
		}

		cancel() // cancel the child context to free resources
	}
}

// putResizedImage is a helper which resizes the provided image to the target width,
// encodes it to jpeg, and uploads it to object storage at the specified key.
// Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) resizeAndPut(ctx context.Context, src image.Image, targetWidth int, objKey string, fileType string) error {

	// resize the image to the target width, maintaining aspect ratio
	resized := resizeImageToWidth(src, targetWidth)
	encoded, err := encodeToJpeg(resized, JpegQuality)
	if err != nil {
		return fmt.Errorf("failed to encode resized image to jpeg for object %s: %v", objKey, err)
	}

	// upload the resized image to object storage at the specified key
	if err := p.objStore.PutObject(ctx, objKey, encoded, fileType); err != nil {
		return fmt.Errorf("failed to upload resized image %s to object storage: %v", objKey, err)
	}

	return nil
}

// getImageRecord is a help retrieves the image record from the database using the provided object key.
// Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) getImageRecord(slug string) (*api.ImageRecord, error) {

	// validate slug
	// redundant check, but good practice
	if err := validate.ValidateUuid(slug); err != nil {
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

	if !api.ValidateAlbumTitle(title) {
		return fmt.Errorf("invalid album title: %s", title)
	}

	if img == nil {
		return fmt.Errorf("image record is nil")
	}

	if err := validate.ValidateUuid(img.Id); err != nil {
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
