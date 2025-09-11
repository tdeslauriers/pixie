package pipeline

import (
	"database/sql"
	"fmt"
	"image"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
	"github.com/tdeslauriers/pixie/pkg/crypt"
)

// source set resolution widths
var ResolutionWidths []int = []int{256, 512, 768, 1024, 1536}

// ImagePipline provides methods for processing image files submitted to the pipeline.
type ImagePipline interface {

	// ProcessImages processes images submitted to the pipeline queue, parsing the webhook,
	// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
	// directory in object storage, typically based on the image year date.
	ProcessQueue()
}

// NewImagePipeline creates a new instance of ImageProcessor, returning
// a pointer to the concrete implementation.
func NewImagePipeline(
	q chan storage.WebhookPutObject,
	wg *sync.WaitGroup,
	db data.SqlRepository,
	i data.Indexer,
	c data.Cryptor,
	o storage.ObjectStorage,
) ImagePipline {

	return &imagePipeline{
		queue: q,
		wg:    wg,

		db:       db,
		indexer:  i,
		cryptor:  crypt.NewCryptor(c),
		objStore: o,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.PackageKey, util.PackagePipeline)).
			With(slog.String(util.ComponentKey, util.ComponentImageProcessor)),
	}
}

var _ ImagePipline = (*imagePipeline)(nil)

// imagePipeline is the concrete implementation of the ImageProcessor interface, which
// provides methods for processing image files submitted to the pipeline.
type imagePipeline struct {
	queue chan storage.WebhookPutObject
	wg    *sync.WaitGroup

	db       data.SqlRepository
	indexer  data.Indexer
	cryptor  crypt.Cryptor
	objStore storage.ObjectStorage

	logger *slog.Logger
}

// ProcessImages is a concrete implementation of the interface method which
// processes images submitted to the pipeline queue, parsing the webhook,
// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
// directory in object storage, typically based on the image year date.
func (p *imagePipeline) ProcessQueue() {

	defer p.wg.Done()

	for webhook := range p.queue {

		// validate webhook
		// redundant check, but good practice
		if err := webhook.Validate(); err != nil {
			p.logger.Error(fmt.Sprintf("invalid webhook received in image processing pipeline: %v", err))
			continue
		}

		p.logger.Info(fmt.Sprintf("processing image upload notification for object %s in bucket %s", webhook.MinioKey, webhook.Records[0].S3.Bucket.Name))

		// parse and validate the object key to get the slug
		// the object key is in the format of "directory/{slug}.extension"
		dir, file, ext, slug, err := p.parseObjectKey(webhook.MinioKey)
		if err != nil {
			p.logger.Error(fmt.Sprintf("failed to parse object key %s from webhook: %v", webhook.MinioKey, err))
			continue
		}

		uploadKey := fmt.Sprintf("%s/%s", dir, file)

		// stream the image file from object storage
		// process the image (read exif, generate thumbnails, etc)
		// move the image to the correct directory in object storage based on the image date or current year
		// if no image date found in exif data
		err = p.objStore.WithObject(uploadKey, func(r storage.ReadSeekCloser) error {

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
			fmt.Printf("OBJECT KEY: %s\n", img.ObjectKey)

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

			// generate and upload the different resolution images for src set
			for _, width := range ResolutionWidths {

				// resize the image to the target width, maintaining aspect ratio
				resized := resizeImageToWidth(src, width)
				encoded, err := encodeToJpeg(resized, JpegQuality)
				if err != nil {
					return fmt.Errorf("failed to encode resized image to jpeg for object %s: %v", img.ObjectKey, err)
				}

				// upload the resized image to object storage in the same directory as the original image
				resizedKey := fmt.Sprintf("%s/%s_w%d%s", filepath.Dir(img.ObjectKey), slug, width, ext)
				if err := p.objStore.PutObject(resizedKey, encoded, img.FileType); err != nil {
					return fmt.Errorf("failed to upload resized image %s to object storage: %v", resizedKey, err)
				}
			}

			// generate and upload blur/placeholder image (hard downscale -> soft blur)
			blur := resizeToLongestSide(src)
			encoded, err := encodeToJpeg(blur, JpegQuality)
			if err != nil {
				return fmt.Errorf("failed to encode blur/placeholder image to jpeg for object %s: %v", img.ObjectKey, err)
			}

			// upload the blur/placeholder image to object storage in the same directory as the original image
			blurKey := fmt.Sprintf("%s/%s_blur%s", filepath.Dir(img.ObjectKey), slug, ext)
			if err := p.objStore.PutObject(blurKey, encoded, img.FileType); err != nil {
				return fmt.Errorf("failed to upload blur/placeholder image %s to object storage: %v", blurKey, err)
			}

			// move the image to the correct directory in object storage
			if err := p.objStore.MoveObject(uploadKey, img.ObjectKey); err != nil {
				return fmt.Errorf("failed to move object %s to new location %s in object storage: %v", webhook.MinioKey, img.ObjectKey, err)
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

			p.logger.Info(fmt.Sprintf("successfully processed image with slug %s", slug))

			return nil
		})
		if err != nil {
			p.logger.Error(fmt.Sprintf("failed to process image %s: %v", uploadKey, err))
			continue
		}

	}
}

// parseObjectKey is a helper which parses the object key from the webhook
// to extract the directory, file name, file extension, and slug.
// Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) parseObjectKey(objectKey string) (dir, file, ext, slug string, err error) {

	// validate object key
	if objectKey == "" {
		return "", "", "", "", fmt.Errorf("object key is empty")
	}

	// get the directory from the object key
	dir = filepath.Dir(objectKey)
	if dir != "gallerydev/uploads" {
		return "", "", "", "", fmt.Errorf("invalid directory in object key: %s", objectKey)
	}

	// drop the bucket name from the directory if it exists
	if strings.Contains(dir, "/") {
		parts := strings.SplitN(dir, "/", 2)
		dir = parts[1]
	}

	// get the file name from the object key
	file = filepath.Base(objectKey)
	if file == "" {
		return "", "", "", "", fmt.Errorf("file name is empty in object key: %s", objectKey)
	}

	// get the file extension from the file name
	ext = filepath.Ext(file)
	if ext == "" || !api.IsValidExtension(ext) {
		return "", "", "", "", fmt.Errorf("file extension must not be empty and must be a valid file type: %s", objectKey)
	}

	// get the slug from the file name
	slug = strings.TrimSuffix(file, ext)
	if slug == "" || !validate.IsValidUuid(slug) {
		return "", "", "", "", fmt.Errorf("invalid slug in object key: %s", objectKey)
	}

	return dir, file, ext, slug, nil
}

// getImageRecord is a help retrieves the image record from the database using the provided object key.
// Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) getImageRecord(slug string) (*db.ImageRecord, error) {

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

	// query the database for the image record
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
		WHERE slug_index = ?;`
	var record db.ImageRecord
	if err := p.db.SelectRecord(qry, &record, index); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no image record found for slug %s", slug)
		}
		return nil, fmt.Errorf("failed to query image record for slug %s: %v", slug, err)
	}

	// decrypt the image record fields
	if err := p.cryptor.DecryptImageRecord(&record); err != nil {
		return nil, fmt.Errorf("failed to decrypt image record for slug %s: %v", slug, err)
	}

	return &record, nil
}

// updateImageRecord is a helper which updates the image record in the database based on
// the exif data if present. Exists to abstract away this logic from the main processing loop.
func (p *imagePipeline) updateImageRecord(img *db.ImageRecord) error {

	// validate image record
	if img == nil {
		return fmt.Errorf("image record is nil")
	}

	// encrypt the image record fields before updating
	if err := p.cryptor.EncryptImageRecord(img); err != nil {
		return fmt.Errorf("failed to encrypt image record for image with id %s: %v", img.Id, err)
	}

	// update the image record in the database
	qry := `
		UPDATE image 
		SET 
			object_key = ?,
			width = ?,
			height = ?,
			image_date = ?,
			updated_at = ?,
			is_published = ?
		WHERE uuid = ?`
	if err := p.db.UpdateRecord(qry,
		img.ObjectKey,
		img.Width,
		img.Height,
		img.ImageDate,
		data.CustomTime{Time: time.Now().UTC()},
		img.IsPublished,
		img.Id); err != nil {

		return fmt.Errorf("failed to update image record in database for image with id %s: %v", img.Id, err)
	}

	return nil
}

// getImageAlbums is a helper which retrieves the album records associated with the image
// using the provided image UUID. Exists to abstract away this logic from the main processing loop
// for readability.  It returns a map for easy lookups of album titles.
func (p *imagePipeline) getImageAlbums(imageUuid string) (map[string]struct{}, error) {

	// get albums associated with the image, if any -> possible none associated yet
	qry := `
		SELECT 
			a.uuid,
			a.title,
			a.description,
			a.slug,
			a.slug_index,
			a.created_at,
			a.updated_at,
			a.is_archived
		FROM album a
			LEFT OUTER JOIN album_image ai ON a.uuid = ai.album_uuid
		WHERE ai.image_uuid = ?;`
	var albums []db.AlbumRecord
	if err := p.db.SelectRecords(qry, &albums, imageUuid); err != nil {
		return nil, fmt.Errorf("failed to query albums for image id - %s: %v", imageUuid, err)
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
func (p *imagePipeline) linkToAlbum(title string, img *db.ImageRecord) error {

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

	// get album records
	qry := `
		SELECT 
			uuid,
			title,
			description,
			slug,
			slug_index,
			created_at,
			updated_at,
			is_archived
		FROM album`
	var album []db.AlbumRecord
	if err := p.db.SelectRecords(qry, &album); err != nil {
		return fmt.Errorf("failed to query all album records: %v", err)
	}

	albumMap := make(map[string]db.AlbumRecord, len(album))
	for _, a := range album {
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
		newAlbum := db.AlbumRecord{
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
		qry := `
			INSERT INTO album (
				uuid,
				title,
				description,
				slug,
				slug_index,
				created_at,
				updated_at,
				is_archived
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?);`
		if err := p.db.InsertRecord(qry, newAlbum); err != nil {
			return fmt.Errorf("failed to insert new album record for album with title %s: %v", title, err)
		}

		// set the albumId to the new album's uuid
		albumId = newAlbum.Id
	}

	// link the image to the album in the album_image xref table
	qry = `
		INSERT INTO album_image (
			id,
			album_uuid,
			image_uuid,
			created_at
		) VALUES (?, ?, ?, ?);`
	xref := db.AlbumImageXref{
		Id:        0,
		AlbumId:   albumId,
		ImageId:   img.Id,
		CreatedAt: data.CustomTime{Time: time.Now().UTC()},
	}
	if err := p.db.InsertRecord(qry, xref); err != nil {
		return fmt.Errorf("failed to link image with id %s to album with id %s: %v", img.Id, albumId, err)
	}

	return nil
}
