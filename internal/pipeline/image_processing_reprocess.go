package pipeline

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect/telemetry"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
)

const (
	// MaxReprocessRetries is the maximum number of attempts for a reprocess command
	// before it is dropped from the queue.
	MaxReprocessRetries int = 5

	// ReprocessBaseBackoff is the delay before the first retry of a failed
	// reprocess command; it doubles on each subsequent retry.
	ReprocessBaseBackoff = 3 * time.Second

	// ReprocessMaxBackoff caps the exponential retry backoff.
	ReprocessMaxBackoff = 1 * time.Minute
)

// ReprocessQueue is a concrete implementation of the interface method which
// reprocesses images in the pipeline queue, based on the ReprocessCmd instructions/criteria.
// It is primarily used for reprocessing images that may have failed initial processing, such as images
// that were uploaded without exif data and landed in staging.  It is also called in order to generate any
// missing image resolutions or tile resolutions that errored upon initial processing.
func (p *imagePipeline) ReprocessQueue(ctx context.Context) {

	defer p.wg.Done()

	for {
		// block until a reprocess command is received, the channel is closed, or the context is cancelled
		var cmd ReprocessCmd
		select {
		case <-ctx.Done():
			return
		case c, ok := <-p.reprocessQueue:
			if !ok {
				return
			}
			cmd = c
		}

		p.processReprocessCmd(ctx, cmd)
	}
}

// processReprocessCmd processes a single reprocess command: it moves the original
// image and its derived files (resolutions, tiles, blur) to their updated location,
// (re)building any derived files that are missing, and links the image to its
// year-based album.
// Transient failures (object storage, database) are re-queued with backoff up to
// MaxReprocessRetries; deterministic failures (unparseable keys, non-year directories)
// are dropped immediately since retrying cannot succeed.
func (p *imagePipeline) processReprocessCmd(ctx context.Context, cmd ReprocessCmd) {

	// create child context with timeout for processing the command, to prevent hanging.
	// defer guarantees the context is released on every path, including early returns.
	itemCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// generate telemetry -> in this case just a trace parent for web calls
	tel := &telemetry.Telemetry{
		Traceparent: *telemetry.NewTraceparent(),
	}

	log := p.logger.With(tel.TelemetryFields()...).With(
		slog.String("image_slug", cmd.Slug),
		slog.Int("attempt", cmd.RetryCount),
	)

	// TODO: add validation here if ever needed.
	// at the moment, all slugs, objkeys, etc., all come from db values, not user input.

	// check if retries exhausted before processing the command
	// Note: requeueReprocess also checks before re-queueing
	if cmd.RetryCount >= MaxReprocessRetries {
		log.Error("max retries reached for reprocess command, dropping",
			slog.Int("max_retries", MaxReprocessRetries))
		return
	}

	// check whether a file move is required
	// Note: current state: a move is always required but this may change in the future
	if !cmd.MoveRequired {
		log.Info("no move required for reprocess command, nothing to do")
		return
	}

	// for now, mvp is just to move the file and fix/add any missing resolutions/tiles
	log.Info("reprocessing image",
		slog.String("current_key", cmd.CurrentObjKey),
		slog.String("updated_key", cmd.UpdatedObjKey),
	)

	// parse the existing/previous key so the resolution file names can be derived.
	// deterministic -> retrying an unparseable key cannot succeed, so drop on failure.
	dir, _, ext, slug, err := ParseObjectKey(cmd.CurrentObjKey)
	if err != nil {
		log.Error("failed to parse existing object key, dropping reprocess command",
			slog.String("current_key", cmd.CurrentObjKey),
			slog.String("err", err.Error()))
		return
	}

	// move the original object to the new location.
	// this must be done first before moving/building resolutions/tiles in order
	// to validate the object exists in the first place.
	// if it does not exists, or fails to move, there is no point in continuing.
	if err := p.objStore.MoveObject(itemCtx, cmd.CurrentObjKey, cmd.UpdatedObjKey); err != nil {

		if strings.Contains(err.Error(), "does not exist in object storage") {

			// the original may already have been moved by a prior attempt that
			// failed partway through -> check the destination before assuming
			// the object is lost, so retries of partial successes are idempotent
			found, lerr := p.objStore.ListObjects(itemCtx, cmd.UpdatedObjKey)
			if lerr == nil && len(found) > 0 {
				log.Info(
					"original image already at updated location, continuing",
					slog.String("updated_key", cmd.UpdatedObjKey),
				)
			} else {

				log.Error(
					"original image not found at current or updated location",
					slog.String("current_key", cmd.CurrentObjKey),
					slog.String("updated_key", cmd.UpdatedObjKey),
					slog.String("err", err.Error()),
				)
				p.requeueReprocess(ctx, log, cmd)
				return
			}
		} else {

			log.Error(
				"failed to move original image to updated location",
				slog.String("current_key", cmd.CurrentObjKey),
				slog.String("updated_key", cmd.UpdatedObjKey),
				slog.String("err", err.Error()),
			)
			p.requeueReprocess(ctx, log, cmd)
			return
		}
	} else {

		log.Info(
			"successfully moved original image to updated location",
			slog.String("current_key", cmd.CurrentObjKey),
			slog.String("updated_key", cmd.UpdatedObjKey),
		)
	}

	// concurrently move and/or (re)build the derived files: resolutions, tiles, and blur
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
			existingResKey := fmt.Sprintf("%s/%s_w%d%s", dir, slug, w, ext)
			updatedResKey := fmt.Sprintf("%s/%s_w%d%s", filepath.Dir(c.UpdatedObjKey), slug, w, ext)

			// the object should already exist, try to move it
			if err := p.objStore.MoveObject(itemCtx, existingResKey, updatedResKey); err != nil {

				// if it does not exist, need to build it
				if strings.Contains(err.Error(), "does not exist in object storage") {

					log.Warn(
						"resolution image not found in object storage, (re)building",
						slog.String("existing_key", existingResKey),
						slog.Int("width", w),
					)

					// stream the original image from object storage + create the resolution
					if err := p.objStore.WithObject(itemCtx, c.UpdatedObjKey, func(r storage.ReadSeekCloser) error {

						// decode the image
						src, _, err := image.Decode(r)
						if err != nil {
							return fmt.Errorf("failed to image-format-decode (jpeg/png) object %s: %v", c.UpdatedObjKey, err)
						}

						// resize the image to the target width, maintaining aspect ratio,
						// encode to jpeg, and upload to object storage
						if err := p.resizeAndPut(itemCtx, src, w, updatedResKey, c.FileType); err != nil {
							return fmt.Errorf("failed to upload resized resolution image %s: %v", updatedResKey, err)
						}

						return nil
					}); err != nil {
						ch <- fmt.Errorf("failed to (re)build resolution image (width %d) from %s: %v", w, c.UpdatedObjKey, err)
						return
					}

					log.Info(
						"successfully (re)built resolution image",
						slog.String("updated_key", updatedResKey),
						slog.Int("width", w),
					)
					return
				}

				ch <- fmt.Errorf("failed to move resolution image %s to %s: %v", existingResKey, updatedResKey, err)
				return
			}

			// successfully moved existing resolution image
			log.Info(
				"successfully moved resolution image",
				slog.String("existing_key", existingResKey),
				slog.String("updated_key", updatedResKey),
			)
		}(&cmd, width, errCh, &wg)
	}

	// loop thru tile widths: move existing and build missing
	for _, width := range util.ResolutionWidthsTiles {

		wg.Add(1)
		go func(c *ReprocessCmd, w int, ch chan error, wg *sync.WaitGroup) {

			defer wg.Done()

			// derive existing/updated tile object key -> based on file naming convention in object storage
			existingTileKey := fmt.Sprintf("%s/%s_tile_w%d%s", dir, slug, w, ext)
			updatedTileKey := fmt.Sprintf("%s/%s_tile_w%d%s", filepath.Dir(c.UpdatedObjKey), slug, w, ext)

			// the object should already exist, try to move it
			if err := p.objStore.MoveObject(itemCtx, existingTileKey, updatedTileKey); err != nil {

				// if it does not exist, need to build it
				if strings.Contains(err.Error(), "does not exist in object storage") {

					log.Warn(
						"tile image not found in object storage, (re)building",
						slog.String("existing_key", existingTileKey),
						slog.Int("width", w),
					)

					// stream the original image from object storage + create the tile
					if err := p.objStore.WithObject(itemCtx, c.UpdatedObjKey, func(r storage.ReadSeekCloser) error {

						// decode the image
						src, _, err := image.Decode(r)
						if err != nil {
							return fmt.Errorf("failed to image-format-decode (jpeg/png) object %s: %v", c.UpdatedObjKey, err)
						}

						// resize the image to the target width, maintaining aspect ratio,
						// encode to jpeg, and upload to object storage
						if err := p.resizeAndPut(itemCtx, src, w, updatedTileKey, c.FileType); err != nil {
							return fmt.Errorf("failed to upload resized tile image %s: %v", updatedTileKey, err)
						}

						return nil
					}); err != nil {
						ch <- fmt.Errorf("failed to (re)build tile image (width %d) from %s: %v", w, c.UpdatedObjKey, err)
						return
					}

					log.Info(
						"successfully (re)built tile image",
						slog.String("updated_key", updatedTileKey),
						slog.Int("width", w),
					)
					return
				}

				ch <- fmt.Errorf("failed to move tile image %s to %s: %v", existingTileKey, updatedTileKey, err)
				return
			}

			// successfully moved existing tile image
			log.Info(
				"successfully moved tile image",
				slog.String("existing_key", existingTileKey),
				slog.String("updated_key", updatedTileKey),
			)
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
		if err := p.objStore.MoveObject(itemCtx, existingBlurKey, updatedBlurKey); err != nil {

			// if it does not exist, need to build it
			if strings.Contains(err.Error(), "does not exist in object storage") {

				log.Warn(
					"blur/placeholder image not found in object storage, (re)building",
					slog.String("existing_key", existingBlurKey),
				)

				// stream the original image from object storage + create the blur/placeholder
				if err := p.objStore.WithObject(itemCtx, c.UpdatedObjKey, func(r storage.ReadSeekCloser) error {

					// decode the image
					src, _, err := image.Decode(r)
					if err != nil {
						return fmt.Errorf("failed to image-format-decode (jpeg/png) object %s: %v", c.UpdatedObjKey, err)
					}

					// generate blur/placeholder image
					blur := resizeToLongestSide(src)
					encoded, err := encodeToJpeg(blur, JpegQuality)
					if err != nil {
						return fmt.Errorf("failed to encode blur/placeholder image to jpeg for object %s: %v", c.UpdatedObjKey, err)
					}

					// upload the blur/placeholder image to object storage in the same directory as the original image
					if err := p.objStore.PutObject(itemCtx, updatedBlurKey, encoded, c.FileType); err != nil {
						return fmt.Errorf("failed to upload blur/placeholder image %s: %v", updatedBlurKey, err)
					}

					return nil
				}); err != nil {
					ch <- fmt.Errorf("failed to (re)build blur/placeholder image from %s: %v", c.UpdatedObjKey, err)
					return
				}

				log.Info(
					"successfully (re)built blur/placeholder image",
					slog.String("updated_key", updatedBlurKey),
				)
				return
			}

			ch <- fmt.Errorf("failed to move blur/placeholder image %s to %s: %v", existingBlurKey, updatedBlurKey, err)
			return
		}

		// successfully moved existing blur/placeholder image
		log.Info(
			"successfully moved blur/placeholder image",
			slog.String("existing_key", existingBlurKey),
			slog.String("updated_key", updatedBlurKey),
		)
	}(&cmd, errCh, &wg)

	// wait for all goroutines to finish
	wg.Wait()
	close(errCh)

	// check for errors from goroutines -> transient (object storage) -> re-queue
	if len(errCh) > 0 {
		errs := make([]error, 0, len(errCh))
		for err := range errCh {
			errs = append(errs, err)
		}
		log.Error(
			"one or more errors occurred moving/(re)building derived images",
			slog.String("err", errors.Join(errs...).Error()),
		)

		p.requeueReprocess(ctx, log, cmd)
		return
	}

	log.Info("successfully moved/(re)built all derived images")

	// ensure image is linked to a year-based album -> if the image is already linked, this is a no-op

	// ensure that there is an album associated with the year if applicable.
	// note: if made it this far, that should mean the move(s) was successful.
	// parse the updated object key to get the directory/year.
	// deterministic -> a key that cannot parse now will never parse, so drop on failure.
	year, _, _, _, err := ParseObjectKey(cmd.UpdatedObjKey)
	if err != nil {

		log.Error(
			"failed to parse updated object key, dropping reprocess command",
			slog.String("updated_key", cmd.UpdatedObjKey),
			slog.String("err", err.Error()),
		)

		return
	}

	// by naming convention, the directory of a published image should be the year,
	// so it should parse to a number.
	// deterministic -> a non-year directory will never become a year, so drop on failure.
	if _, err := strconv.Atoi(year); err != nil {

		log.Error(
			"directory parsed from updated object key is not a valid year, dropping reprocess command",
			slog.String("directory", year),
			slog.String("updated_key", cmd.UpdatedObjKey),
			slog.String("err", err.Error()),
		)

		return
	}

	// create xref -> database call -> transient -> re-queue on failure.
	// note: linkToAlbum uses impl of xref insert that is idempotent -> if the xref already exists, it is a no-op.
	// failures here are likely transient (database connection, etc.) and should be retried.
	if err := p.linkToAlbum(year, &api.ImageRecord{Id: cmd.Id}); err != nil {

		log.Error(
			"failed to create album xref for image",
			slog.String("year", year),
			slog.String("image_id", cmd.Id),
			slog.String("err", err.Error()),
		)

		p.requeueReprocess(ctx, log, cmd)
		return
	}

	log.Info("successfully reprocessed image")
}

// requeueReprocess increments a failed command's retry count and re-queues it for
// another attempt after an exponential backoff delay, dropping it if max retries
// have been exhausted.
// The delayed send runs in its own goroutine (tracked in the service WaitGroup so
// shutdown waits for pending re-queues) because the processor sending to its own
// full queue from the processing path would deadlock because nothing drains the channel
// while the only consumer is blocked on the send.
// Note: takes the parent ctx, not the per-item ctx, which is cancelled when the
// processing function returns.
func (p *imagePipeline) requeueReprocess(ctx context.Context, log *slog.Logger, cmd ReprocessCmd) {

	cmd.RetryCount++

	// check if retries exhausted
	if cmd.RetryCount >= MaxReprocessRetries {
		log.Error("max retries reached for reprocess command, dropping",
			slog.Int("max_retries", MaxReprocessRetries))
		return
	}

	// add backoff and jitter
	delay := reprocessBackoff(cmd.RetryCount)
	log.Warn(
		"re-queueing reprocess command for retry",
		slog.Int("next_attempt", cmd.RetryCount),
		slog.Duration("backoff", delay),
	)

	// track the pending re-queue in the service WaitGroup so graceful shutdown
	// waits for it -> prevents a send on a channel closed during shutdown
	p.wg.Add(1)
	go func() {

		defer p.wg.Done()

		t := time.NewTimer(delay)
		defer t.Stop()

		// wait out the backoff, bailing on shutdown (parent ctx cancelled) to avoid sending on a closed channel
		// this select is a race between the parent ctx being cancelled (ie shutdown) and the timer expiring,
		// so the send is only attempted if the timer wins
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		select {
		case <-ctx.Done():
		case p.reprocessQueue <- cmd:
		}
	}()
}

// reprocessBackoff returns the exponential backoff delay for a given retry attempt:
// base * 2^(attempt-1), capped at ReprocessMaxBackoff.
func reprocessBackoff(attempt int) time.Duration {

	if attempt < 1 {
		attempt = 1
	}

	// exponential: slide the base's bits left one place per attempt -> doubles each time
	d := ReprocessBaseBackoff << uint(attempt-1)
	if d <= 0 || d > ReprocessMaxBackoff { // <= 0 catches overflow
		d = ReprocessMaxBackoff
	}

	// equal jitter: keep half the delay deterministic, randomize the other half.
	// preserves the exponential shape and the max cap (result is always < d),
	// while de-synchronizing retries that were scheduled at the same instant.
	half := d / 2
	if half <= 0 {
		return d // guard: rand.N panics on non-positive input
	}

	return half + rand.N(half) // crypto rand unnecessary here, just need to de-sync retries, not secure randomness
}
