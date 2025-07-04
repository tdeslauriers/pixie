package image

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/pixie/internal/util"
)

var (
	readImagesAllowed  = []string{"r:pixie:images:*"}
	writeImagesAllowed = []string{"w:pixie:images:*"}
)

// Handler is the interface for image processing service handlers.
// It defines the methods that any image handler must implement to process image requests.
type Handler interface {

	// HandleImage handles the image processing request
	HandleImage(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new image handler instance, returning a pointer to the concrete implementation.
func NewHandler(s Service, s2s, iam jwt.Verifier) Handler {
	return &imageHandler{
		svc: s,
		s2s: s2s,
		iam: iam,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackageImage)).
			With(slog.String(util.ComponentKey, util.ComponentImage)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ Handler = (*imageHandler)(nil)

// imageHandler is a concrete implementation of the Handler interface.
type imageHandler struct {
	svc Service // The image service instance to handle image processing tasks
	s2s jwt.Verifier
	iam jwt.Verifier

	logger *slog.Logger
}

// HandleImage is the concrete implementation of the HandleImage method.
func (h *imageHandler) HandleImage(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		// Handle GET request for image processing
		h.getImageData(w, r)
		return
	case http.MethodPost: // /images/upload --> 'upload' will be the slug for POSTs
		// Handle POST request for image processing
		h.handleAddImageRecord(w, r)
		return
	default:
		errMsg := fmt.Sprintf("unsupported method %s for image handler", r.Method)
		h.logger.Error(errMsg)
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    errMsg,
		}
		e.SendJsonErr(w)
	}
}

// getImageData handles the GET request for image processing.
func (h *imageHandler) getImageData(w http.ResponseWriter, r *http.Request) {

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(readImagesAllowed, svcToken); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	if _, err := h.iam.BuildAuthorized(readImagesAllowed, accessToken); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	slug, err := connect.GetValidSlug(r)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get valid slug: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to get valid slug",
		}
		e.SendJsonErr(w)
		return
	}

	imageData, err := h.svc.GetImageData(slug)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to get image data",
		}
		e.SendJsonErr(w)
		return
	}

	// TODO: once permissions are added, make it so only admins can see unpublished/archived images

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(imageData); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to encode image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode image data",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleAddImageRecord handles the POST request for adding an image record.
// It intakes and validates the incoming data, then processes it to create a new image record in the database.
// It also generates a signed URL for the image in object storage to return to the client for uploading the image file.
// This mechanism is based on the file processsing pipeline pattern.
func (h *imageHandler) handleAddImageRecord(w http.ResponseWriter, r *http.Request) {

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(writeImagesAllowed, svcToken); err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	if _, err := h.iam.BuildAuthorized(writeImagesAllowed, accessToken); err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// decode the request body into the AddMetaDataCmd struct
	var cmd AddMetaDataCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to decode request body: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the incoming data
	if err := cmd.Validate(); err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to validate incoming data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("invalid image metadata: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// build placeholder image record waiting for the image file to be processed on
	// notification from the object storage service
	id, err := uuid.NewRandom()
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to generate new image record id: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to generate new image record id",
		}
		e.SendJsonErr(w)
		return
	}

	// create the slug (which is a unique identifier shared as the filename in object storage)
	slug, err := uuid.NewRandom()
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to generate new image slug: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to generate new image slug",
		}
		e.SendJsonErr(w)
		return
	}

	// get file type from the command --> extension
	ext, err := cmd.GetExtension()
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to get file extension from command: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("failed to get file extension from command: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// build the filename for the image file
	// this should not change even if the namespace changes in the object storage service
	fileName := fmt.Sprintf("%s.%s", slug.String(), ext)

	// build the object key for the image file in object storage
	objectKey := fmt.Sprintf("uploads/%s", fileName)

	now := time.Now().UTC()

	// incomplete/stubbed image record missing fields that will be filled in later
	// when the image file is processed and the object storage service notifies the image service
	imageRecord := ImageRecord{
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

	// persist the image record in the database and
	// generate a pre-signed PUT URL to return for browser to submit the file to object storage
	putUrl, err := h.svc.BuildPlaceholder(&imageRecord)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images handler failed to build placeholder image record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to build placeholder image record",
		}
		e.SendJsonErr(w)
		return
	}

	// TODO: add the pre-added album mappings from the upload command. --> may also included adding album record.
	// TODO: add the pre-added permissions tags from the upload command.

	// build reponse data to return to the client
	responseData := ImageData{
		Id:          imageRecord.Id,
		Title:       imageRecord.Title,
		Description: imageRecord.Description,
		FileName:    imageRecord.FileName,
		FileType:    imageRecord.FileType,
		ObjectKey:   imageRecord.ObjectKey, // this is the "uploads/slug.jpg" key in object storage -> staging
		Slug:        imageRecord.Slug,
		Size:        imageRecord.Size,
		CreatedAt:   imageRecord.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   imageRecord.UpdatedAt.Format(time.RFC3339), // format the time as RFC3339
		IsArchived:  imageRecord.IsArchived,
		IsPublished: imageRecord.IsPublished,
		SignedUrl:   putUrl.String(), // the pre-signed PUT URL for the browser to upload the image file into object storage
	}

	h.logger.Info(fmt.Sprintf("/images/ handler successfully created placeholder image record with id %s", imageRecord.Id))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(responseData); err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to encode placeholder image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to json encode placeholder image data",
		}
		e.SendJsonErr(w)
		return
	}
}
