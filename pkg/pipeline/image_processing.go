package pipeline

import (
	"log/slog"
	"sync"

	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
)

// ImagePipline provides methods for processing image files submitted to the pipeline.
type ImagePipline interface {

	// ProcessImages processes images submitted to the pipeline queue, parsing the webhook,
	// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
	// directory in object storage, typically based on the image year date.
	ProcessQueue()
}

// NewImagePipeline creates a new instance of ImageProcessor, returning
// a pointer to the concrete implementation.
func NewImagePipeline(q chan storage.WebhookPutObject, wg *sync.WaitGroup) ImagePipline {

	return &imagePipeline{
		queue: q,
		wg:    wg,

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

	logger *slog.Logger
}

// ProcessImages is a concrete implementation of the interface method which
// processes images submitted to the pipeline queue, parsing the webhook,
// reading the exif data if it exists, generating thumbnails, and moving the image to the correct
// directory in object storage, typically based on the image year date.
func (p *imagePipeline) ProcessQueue() {

	defer p.wg.Done()

	for webhook := range p.queue {

		// TODO: implement image processing logic here
		// parse the webhook
		// read the exif data if it exists
		// generate thumbnails + blur/placeholder images
		// move the image to the correct directory in object storage

	}
}
