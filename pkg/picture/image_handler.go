package picture

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
	"github.com/tdeslauriers/pixie/pkg/permission"
)

var (
	readImagesAllowed  = []string{"r:pixie:*", "r:pixie:images:*"}
	writeImagesAllowed = []string{"w:pixie:*", "w:pixie:images:*"}
)

// Handler is the interface for image processing service handlers.
// It defines the methods that any image handler must implement to process image requests.
type ImageHandler interface {

	// HandleImage handles the image processing request
	HandleImage(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new image handler instance, returning a pointer to the concrete implementation.
func NewImageHandler(s Service, p permission.Service, s2s, iam jwt.Verifier) ImageHandler {
	return &imageHandler{
		svc:   s,
		perms: p,
		s2s:   s2s,
		iam:   iam,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ComponentKey, util.ComponentImage)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ ImageHandler = (*imageHandler)(nil)

// imageHandler is a concrete implementation of the Handler interface.
type imageHandler struct {
	svc   Service
	perms permission.Service
	s2s   jwt.Verifier
	iam   jwt.Verifier

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
	authorized, err := h.iam.BuildAuthorized(readImagesAllowed, accessToken)
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

	// get user permissions
	usrPsMap, _, err := h.perms.GetPatronPermissions(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get permissions for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to get user permissions",
		}
		e.SendJsonErr(w)
		return
	}

	imageData, err := h.svc.GetImageData(slug, usrPsMap)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get image data: %v", err))
		h.svc.HandleImageServiceError(err, w)
		return
	}

	// if user is a curator/admin, need to return the album and permission xrefs
	if _, ok := usrPsMap[util.PermissionCurator]; ok {

		_, albumRecords, err := h.svc.GetImageAlbums(imageData.Id)
		if err != nil {
			if strings.Contains(err.Error(), "no albums found") {
				// no albums found is not an error, log it as a warning
				h.logger.Warn(fmt.Sprintf("/images/slug handler no albums found for image '%s': %v", imageData.Slug, err))
			} else {
				errMsg := fmt.Sprintf("/images/slug handler failed to get albums for image '%s': %v", imageData.Slug, err)
				h.logger.Error(errMsg)
				// TODO once shunt to go routine, handle as channel err
			}
		}

		albums, err := mapAlbumRecordsToApi(albumRecords)
		if err != nil {
			h.logger.Error(fmt.Sprintf("/images/slug handler failed to map album records to API: %v", err))
			e := connect.ErrorHttp{
				StatusCode: http.StatusInternalServerError,
				Message:    "failed to map album records to API",
			}
			e.SendJsonErr(w)
			return
		}

		imageData.Albums = albums

		// get the permissions associated with the image
		_, permissionRecords, err := h.perms.GetImagePermissions(imageData.Id)
		if err != nil {
			if strings.Contains(err.Error(), "no permissions found") {
				// no permissions found is not an error, log it as a warning
				h.logger.Warn(fmt.Sprintf("/images/slug handler no permissions found for image '%s': %v", imageData.Slug, err))
			} else {
				errMsg := fmt.Sprintf("/images/slug handler failed to get permissions for image '%s': %v", imageData.Slug, err)
				h.logger.Error(errMsg)
				// TODO once shunt to go routine, handle as channel err
			}
		}

		permissions, err := permission.MapPermissionRecordsToApi(permissionRecords)
		if err != nil {
			h.logger.Error(fmt.Sprintf("/images/slug handler failed to map permission records to API: %v", err))
			e := connect.ErrorHttp{
				StatusCode: http.StatusInternalServerError,
				Message:    "failed to map permission records to API",
			}
			e.SendJsonErr(w)
			return
		}

		imageData.Permissions = permissions
	}

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
	var cmd api.UpdateMetadataCmd
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

	// get user permissions
	usrPsMap, _, err := h.perms.GetPatronPermissions(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get permissions for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to get user permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// check if the image exists
	existing, err := h.svc.GetImageData(slug, usrPsMap)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/slug handler failed to get image data: %v", err))
		h.svc.HandleImageServiceError(err, w)
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
	updated := &db.ImageRecord{
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
		h.svc.HandleImageServiceError(err, w)
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
	authorized, err := h.iam.BuildAuthorized(writeImagesAllowed, accessToken)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/images/ handler failed to authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// decode the request body into the AddMetaDataCmd struct
	var cmd api.AddMetaDataCmd
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
		h.svc.HandleImageServiceError(err, w)
		return
	}

	// adding album associations is optional at this juncture, so do not wait to return
	go func() {
		// add the pre-added album mappings from the upload command.
		albumIds, err := h.getValidAlbumIds(authorized.Claims.Subject, cmd.Albums)
		if err != nil {
			h.logger.Error(fmt.Sprintf("/images/ handler failed to get valid album slugs: %v", err))
			return // do not block the response, just log the error
		}

		if len(albumIds) > 0 {
			for _, albumId := range albumIds {

				go func(albId, imgId string) {
					// add the image to the album by updating xref table
					if err := h.svc.InsertAlbumImageXref(albId, imgId); err != nil {
						h.logger.Error(fmt.Sprintf("failed to add image to album '%s': %v", albId, err))
						return
					}
					h.logger.Info(fmt.Sprintf("created xref between image '%s' to album '%s'", imgId, albId))
				}(albumId, placeholder.Id)
			}
		}
	}()

	// adding permissions is optional at this juncture, so do not wait to return
	go func() {
		// get all permissions
		psMap, _, err := h.perms.GetAllPermissions()
		if err != nil {
			h.logger.Error(fmt.Sprintf("/images/ handler failed to get permissions for user '%s': %v", authorized.Claims.Subject, err))
			return // do not block the response, just log the error
		}

		// get permission ids to add to the image_permissions table
		var permissionIds []string
		for _, p := range cmd.Permissions {
			if perm, ok := psMap[p.Slug]; ok {
				permissionIds = append(permissionIds, perm.Id)
			} else {
				h.logger.Error(fmt.Sprintf("permission '%s' not found for user '%s'", p, authorized.Claims.Subject))
			}
		}

		for _, permissionId := range permissionIds {

			go func(imgId, permId string) {
				// add the image to the permissions by updating xref table
				if err := h.svc.InsertImagePermissionXref(imgId, permId); err != nil {
					h.logger.Error(fmt.Sprintf("failed to add image to permission '%s': %v", permId, err))
					return
				}
				h.logger.Info(fmt.Sprintf("created xref between image '%s' to permission '%s'", imgId, permId))
			}(placeholder.Id, permissionId)
		}
	}()

	h.logger.Info(fmt.Sprintf("%s successfully created placeholder image record with id %s", authorized.Claims.Subject, placeholder.Id))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(placeholder); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode placeholder image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to json encode placeholder image data",
		}
		e.SendJsonErr(w)
		return
	}
}

// getValidAlbumIds is a helper function that retrieves the valid album IDs from the provided album command data.
func (h *imageHandler) getValidAlbumIds(username string, albumsCmd []api.Album) ([]string, error) {

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(username)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve permissions for user '%s': %v", username, err)
	}

	// get the user's allowed albums
	allowed, _, err := h.svc.GetAllowedAlbums(ps)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve allowed albums for user '%s': %v", username, err)
	}

	ids := make([]string, 0, len(albumsCmd))
	for _, album := range albumsCmd {

		// check if slug exists in the user's allowed albums
		if _, ok := allowed[album.Slug]; !ok {
			return nil, fmt.Errorf("album with slug '%s' not found", album.Slug)
		}

		ids = append(ids, album.Id)
	}

	if len(ids) < 1 {
		return nil, fmt.Errorf("at least one valid album slug is required")
	}

	return ids, nil
}
