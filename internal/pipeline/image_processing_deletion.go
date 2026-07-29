package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect/telemetry"
)

// deletionSweepDirs are the non-year-based "directories" in the bucket where
// image files may have landed during upload/processing, e.g., images uploaded
// without exif data that were parked pending a date.
var deletionSweepDirs = []string{"uploads", "staging"}

// DeletionQueue processes deletion requests for images in the pipeline queue, based on the DeletionCmd instructions/criteria.
// It is primarily used for deleting images that have errored initial and reprocessing such that the database and the minio files
// cannot be reconciled easily.  It can also be used to delete any image but that is a more rare use case and images can
// be archived instead of deleted in most cases.
func (p *imagePipeline) DeletionQueue(ctx context.Context) {

	defer p.wg.Done()

	for {
		var cmd DeletionCmd
		select {
		case <-ctx.Done():
			return
		case c, ok := <-p.deletionQueue:
			if !ok {
				return
			}
			cmd = c
		}

		// process each command in its own function scope so the per-item timeout
		// context is released via defer on every path (avoids context leaks)
		p.processDeletionCmd(ctx, cmd)
	}
}

// processDeletionCmd processes a single deletion command: it sweeps object storage
// for the image's original and derived files across all candidate directories,
// bulk-deletes whatever is found
func (p *imagePipeline) processDeletionCmd(ctx context.Context, cmd DeletionCmd) {

	// create child context with timeout for processing each command, to prevent hanging
	itemCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// generate telemetry -> in this case just a trace parent for web calls
	tel := &telemetry.Telemetry{
		Traceparent: *telemetry.NewTraceparent(),
	}
	log := p.logger.With(tel.TelemetryFields()...)

	// TODO: add validation here if ever needed.
	// at the moment, all slugs, objkeys, etc., all come from db values, not user input.

	log.Info(fmt.Sprintf("processing deletion command for image with slug %s", cmd.Slug))

	// build the candidate prefixes to sweep: the directory parsed from the
	// object key (if parseable), plus the uploads/staged sweep directories.
	prefixes, err := buildDeletionPrefixes(cmd)
	if err != nil {
		log.Error("failed to build deletion prefixes for image", "image_slug", cmd.Slug, "err", err)
		return
	}

	// list all objects matching each prefix.
	// sequential on purpose: a failed listing must abort the whole sweep
	// a partial key set would delete some files and orphan the rest
	keys := make([]string, 0)
	for _, prefix := range prefixes {

		found, err := p.objStore.ListObjects(itemCtx, prefix)
		if err != nil {
			log.Error(
				"failed to list objects with prefix for image",
				"image_prefix", prefix,
				"image_slug", cmd.Slug,
				"err", err.Error(),
			)
			return
		}

		keys = append(keys, found...)
	}

	// delete everything found in a single bulk operation.
	// note: zero keys found is not an error -> the image files may already be gone
	if len(keys) == 0 {

		log.Warn("no objects found to delete for image with slug", "image_slug", cmd.Slug, "prefixes", prefixes)
	} else {

		// delete from object storage
		if err := p.objStore.DeleteObjects(itemCtx, keys); err != nil {
			log.Error("failed to delete objects for image", "image_slug", cmd.Slug, "err", err.Error())
			return
		}

		log.Info("successfully deleted objects for image", "image_slug", cmd.Slug, "deleted_keys_count", len(keys), "keys", keys)
	}
}

// buildDeletionPrefixes builds the set of object key prefixes to sweep for a
// given deletion command. Each prefix is "<dir>/<slug>", which matches the
// original image and every derived file (resolutions, tiles, blur) by naming
// convention -> "<dir>/<slug>.<ext>", "<dir>/<slug>_w1200.<ext>",
// "<dir>/<slug>_tile_w256.<ext>", "<dir>/<slug>_blur.<ext>", etc.
// Because the slug is a full uuid, the prefix cannot partially match any other
// image's files.
func buildDeletionPrefixes(cmd DeletionCmd) ([]string, error) {

	slug := cmd.Slug

	// dedupe candidate directories
	dirs := make(map[string]struct{})

	// if the command carries an object key, parse it for the directory and slug.
	// a failed parse is not fatal here: the object key may be exactly what is broken,
	// which is one of the reasons an image ends up in the deletion queue.
	if cmd.ObjectKey != "" {
		if dir, _, _, s, err := ParseObjectKey(cmd.ObjectKey); err == nil {
			dirs[dir] = struct{}{}
			if slug == "" {
				slug = s
			}
		}
	}

	// the slug is the minimum needed to derive the file naming convention
	if slug == "" {
		return nil, fmt.Errorf("unable to determine slug from deletion command (slug: %q, object key: %q)", cmd.Slug, cmd.ObjectKey)
	}

	// always sweep the non-year directories where files may have been parked
	for _, d := range deletionSweepDirs {
		dirs[d] = struct{}{}
	}

	// build the prefixes
	prefixes := make([]string, 0, len(dirs))
	for dir := range dirs {
		prefixes = append(prefixes, fmt.Sprintf("%s/%s", dir, slug))
	}

	return prefixes, nil
}
