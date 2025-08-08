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
	readImagesAllowed  = []string{"r:pixie:*", "r:pixie:images:*"}
	writeImagesAllowed = []string{"w:pixie:*", "w:pixie:images:*"}
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

		// get the slug to determine if new image record or existing image record
		// get the url slug from the request
		segments := strings.Split(r.URL.Path, "/")

		var slug string
		if len(segments) > 1 {
			slug = segments[len(segments)-1]
		} else {
			h.logger.Error("slug is required for POST request to /images/")
			e := connect.ErrorHttp{
				StatusCode: http.StatusBadRequest,
				Message:    "slug is required for POST request to /images/",
			}
			e.SendJsonErr(w)
			return
		}

		if slug == "upload" {
			h.handleAddImageRecord(w, r)
			return
		} else {
			h.handleUpdateImageRecord(w, r)
			return
		}

	default:
		errMsg := fmt.Sprintf("unsupported method %s for image handler", r.Method)
		h.logger.Error(errMsg)
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    errMsg,
		}
		e.SendJsonErr(w)
		return
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

func (h *imageHandler) handleUpdateImageRecord(w http.ResponseWriter, r *http.Request) {

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(writeImagesAllowed, svcToken); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	authorized, err := h.iam.BuildAuthorized(writeImagesAllowed, accessToken)
	if err != nil {
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

	// get update data from the request body
	var cmd UpdateMetadataCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to decode request body: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the update data
	if err := cmd.Validate(); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to validate update data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("invalid image metadata: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// check if the image exists
	existing, err := h.svc.GetImageData(slug)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusNotFound,
			Message:    fmt.Sprintf("image with slug '%s' not found", slug),
		}
		e.SendJsonErr(w)
		return
	}

	// validate the slugs all match:
	// no risk since the slug will not be overwritten, but good practice to validate
	// and a good check for tampering with the request overall
	if existing.Slug != slug {
		h.logger.Error(fmt.Sprintf("/images/slug handler slug mismatch: expected '%s', got '%s'", existing.Slug, slug))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("slug mismatch: expected '%s', got '%s'", existing.Slug, slug),
		}
		e.SendJsonErr(w)
		return
	}

	if existing.Slug != cmd.Slug {
		h.logger.Error(fmt.Sprintf("/images/slug cmd slug mismatch: expected '%s', got '%s'", existing.Slug, cmd.Slug))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("slug mismatch: expected '%s', got '%s'", existing.Slug, cmd.Slug),
		}
		e.SendJsonErr(w)
		return
	}

	// build image date from the update cmd date fields
	// image data is required, so if any of the fields are empty, validation ^^ will have returned an error
	imageDate := time.Date(cmd.ImageDateYear, time.Month(cmd.ImageDateMonth), cmd.ImageDateDay, 0, 0, 0, 0, time.UTC)

	// update the objectKey (note it may not change, but we still need the value for the update cmd)
	objectKey := fmt.Sprintf("%d/%s", imageDate.Year(), existing.FileName)

	// build image record that are allowed to be updated
	// Note: more fields can be added here as needed
	updated := &ImageRecord{
		Id:          existing.Id, // id should not change
		Title:       cmd.Title,
		Description: cmd.Description,
		FileName:    existing.FileName, // file name should not change
		FileType:    existing.FileType, // file type should not change
		ObjectKey:   objectKey,         // object key may change if err on upload pipeline => unpublished image
		Slug:        existing.Slug,     // slug should not change
		Size:        existing.Size,
		ImageDate:   imageDate.Format(time.RFC3339), // format the image date as RFC3339
		// created_at will not change, and will not be included in the updated fields:  leave default value.
		UpdatedAt:   data.CustomTime{Time: time.Now().UTC()},
		IsArchived:  cmd.IsArchived,
		IsPublished: cmd.IsPublished,
	}

	// handles updating the object store if necessary
	if err := h.svc.UpdateImageData(existing, updated); err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to update image record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to update image record",
		}
		e.SendJsonErr(w)
		return
	}

	// TODO: update albums associated with the image

	// TODO: update permissions associated with the image

	// audit log
	if existing.Title != updated.Title {
		h.logger.Info(fmt.Sprintf("image slug '%s' title updated from '%s' to '%s' by %s", existing.Slug, existing.Title, updated.Title, authorized.Claims.Subject))
	}

	if existing.Description != updated.Description {
		h.logger.Info(fmt.Sprintf("image  slug  '%s' description updated from '%s' to '%s' by %s", existing.Slug, existing.Description, updated.Description, authorized.Claims.Subject))
	}

	if existing.ImageDate != updated.ImageDate {
		h.logger.Info(fmt.Sprintf("image slug '%s' date updated from '%s' to '%s' by %s", existing.Slug, existing.ImageDate, updated.ImageDate, authorized.Claims.Subject))
	}

	if existing.ObjectKey != updated.ObjectKey {
		h.logger.Info(fmt.Sprintf("image slug '%s' object key updated from '%s' to '%s' by %s", existing.Slug, existing.ObjectKey, updated.ObjectKey, authorized.Claims.Subject))
	}

	if existing.IsArchived != updated.IsArchived {
		h.logger.Info(fmt.Sprintf("image slug '%s' archived status updated from '%t' to '%t' by %s", existing.Slug, existing.IsArchived, updated.IsArchived, authorized.Claims.Subject))
	}

	if existing.IsPublished != updated.IsPublished {
		h.logger.Info(fmt.Sprintf("image slug '%s' published status updated from '%t' to '%t' by %s", existing.Slug, existing.IsPublished, updated.IsPublished, authorized.Claims.Subject))
	}

	w.WriteHeader(http.StatusNoContent) // 204 No Content
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
	// generate a pre-signed PUT URL to return for browser to submit the file to object storage
	placeholder, err := h.svc.BuildPlaceholder(cmd)
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


	h.logger.Info(fmt.Sprintf("/images/ handler successfully created placeholder image record with id %s", imageRecord.Id))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(placeholder); err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to encode placeholder image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to json encode placeholder image data",
		}
		e.SendJsonErr(w)
		return
	}
}
