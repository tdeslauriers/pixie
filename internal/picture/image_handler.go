package picture

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/internal/album"
	"github.com/tdeslauriers/pixie/internal/permission"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/api"
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
func NewHandler(
	s Service,
	a album.Service,
	p permission.Service,
	s2s jwt.Verifier,
	iam jwt.Verifier,
) ImageHandler {
	return &imageHandler{
		svc:    s,
		albums: a,
		perms:  p,
		s2s:    s2s,
		iam:    iam,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ComponentKey, util.ComponentImage)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ ImageHandler = (*imageHandler)(nil)

// imageHandler is a concrete implementation of the Handler interface.
type imageHandler struct {
	svc    Service
	albums album.Service
	perms  permission.Service
	s2s    jwt.Verifier
	iam    jwt.Verifier

	logger *slog.Logger
}

// HandleImage is the concrete implementation of the HandleImage method.
func (h *imageHandler) HandleImage(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet: // there is no 'get all' for images
		h.getImageData(w, r)
		return
	case http.MethodPut:
		h.handleUpdateImageRecord(w, r)
		return
	case http.MethodPost: // image upload
		h.handleAddImageRecord(w, r)
		return
	default:
		// Handle unsupported methods
		// get telemetry from request
		tel := connect.ObtainTelemetry(r, h.logger)
		log := h.logger.With(tel.TelemetryFields()...)

		log.Error(fmt.Sprintf("unsupported method %s for endpoint %s", r.Method, r.URL.Path))
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    fmt.Sprintf("unsupported method %s for endpoint %s", r.Method, r.URL.Path),
		}
		e.SendJsonErr(w)
		return
	}
}

// getImageData handles the GET request for image processing.
func (h *imageHandler) getImageData(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(readImagesAllowed, svcToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(readImagesAllowed, accessToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// get slug from request
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		log.Error(fmt.Sprintf("failed to get valid slug: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// get user permissions
	usrPsMap, _, err := h.perms.GetPatronPermissions(ctx, authedUser.Claims.Subject)
	if err != nil {
		log.Error("failed to get user permissions", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to get user permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// get image meta data from database
	imageData, err := h.svc.GetImageData(slug, usrPsMap)
	if err != nil {
		log.Error("failed to get image data", "err", err.Error())
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	// if user is a curator/admin, need to return the album and permission xrefs
	if _, ok := usrPsMap[util.PermissionCurator]; ok {

		type albumsResult struct {
			records []api.AlbumRecord
			err     error
		}
		type permissionsResult struct {
			records []permissions.PermissionRecord
			err     error
		}

		albumsCh := make(chan albumsResult, 1)
		permissionsCh := make(chan permissionsResult, 1)

		// gather albums concurrently
		go func() {
			_, albumRecords, err := h.svc.GetImageAlbums(imageData.Id)
			albumsCh <- albumsResult{records: albumRecords, err: err}
		}()

		// gather permissions concurrently
		go func() {
			_, permissionRecords, err := h.perms.GetImagePermissions(imageData.Id)
			permissionsCh <- permissionsResult{records: permissionRecords, err: err}
		}()

		// wait for both results
		albumsRes := <-albumsCh
		permissionsRes := <-permissionsCh

		// handle albums result
		if albumsRes.err != nil {
			if strings.Contains(albumsRes.err.Error(), "no albums found") {
				log.Warn(fmt.Sprintf("no albums found for image '%s'", imageData.Slug),
					"err", albumsRes.err.Error())
			} else {
				log.Error(fmt.Sprintf("failed to get albums for image '%s'", imageData.Slug),
					"err", albumsRes.err.Error())
			}
		} else {
			albums, err := album.MapAlbumRecordsToApi(albumsRes.records)
			if err != nil {
				log.Error("failed to map album records to album data struct", "err", err.Error())
				e := connect.ErrorHttp{
					StatusCode: http.StatusInternalServerError,
					Message:    "failed to map album records correctly",
				}
				e.SendJsonErr(w)
				return
			}
			imageData.Albums = albums
		}

		// handle permissions result
		if permissionsRes.err != nil {
			if strings.Contains(permissionsRes.err.Error(), "no permissions found") {
				log.Warn(fmt.Sprintf("handler no permissions found for image '%s'", imageData.Slug),
					"err", permissionsRes.err.Error())
			} else {
				log.Error(fmt.Sprintf("/images/slug handler failed to get permissions for image '%s'", imageData.Slug),
					"err", permissionsRes.err.Error())
			}
		} else {
			permissions, err := permission.MapPermissionRecordsToApi(permissionsRes.records)
			if err != nil {
				log.Error("failed to map permission records to permissions data struct", "err", err.Error())
				e := connect.ErrorHttp{
					StatusCode: http.StatusInternalServerError,
					Message:    "failed to correctly map permission records",
				}
				e.SendJsonErr(w)
				return
			}
			imageData.Permissions = permissions
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(imageData); err != nil {
		log.Error("failed to encode image data to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode image data to json",
		}
		e.SendJsonErr(w)
		return
	}
}

func (h *imageHandler) handleUpdateImageRecord(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(writeImagesAllowed, svcToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(writeImagesAllowed, accessToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// get slug from request path
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		log.Error("failed to get valid slug from request path", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// get update data from the request body
	var cmd api.UpdateMetadataCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode request body", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the update data
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate update image metadata command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// get user permissions
	usrPsMap, _, err := h.perms.GetPatronPermissions(ctx, authedUser.Claims.Subject)
	if err != nil {
		log.Error("failed to get user permissions", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to get user permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// check if the image exists -> get from database
	existing, err := h.svc.GetImageData(slug, usrPsMap)
	if err != nil {
		log.Error("failed to get existing image data", "err", err.Error())
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	// validate the slugs all match:
	// no risk since the slug will not be overwritten, but good practice to validate
	// and a good check for tampering with the request overall
	if existing.Slug != slug {
		log.Error(fmt.Sprintf("/images/slug handler slug mismatch: expected '%s', got '%s'", existing.Slug, slug))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("slug mismatch: expected '%s', got '%s'", existing.Slug, slug),
		}
		e.SendJsonErr(w)
		return
	}

	if existing.Slug != cmd.Slug {
		log.Error(fmt.Sprintf("/images/slug cmd slug mismatch: expected '%s', got '%s'", existing.Slug, cmd.Slug))
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
	updated := &api.ImageRecord{
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

	// is update necessary?
	if existing.Title == updated.Title &&
		existing.Description == updated.Description &&
		existing.ImageDate == updated.ImageDate &&
		existing.ObjectKey == updated.ObjectKey &&
		existing.IsArchived == updated.IsArchived &&
		existing.IsPublished == updated.IsPublished {
		log.Warn(fmt.Sprintf("no changes detected for image slug '%s', skipping update", existing.Slug))
		return
	}

	// handles updating the database and the object store if necessary
	if err := h.svc.UpdateImageData(ctx, existing, updated); err != nil {
		log.Error(fmt.Sprintf("/images/slug handler failed to update image data: %v", err))
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	// TODO: add concurrency here if needed
	// update albums associated with the image
	if err := h.svc.UpdateAlbumImages(ctx, existing.Id, cmd.AlbumSlugs); err != nil {
		log.Error(fmt.Sprintf("/images/slug handler failed to update image albums: %v", err))
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	// update permissions associated with the image
	if err := h.perms.UpdateImagePermissions(ctx, existing.Id, cmd.PermissionSlugs); err != nil {
		log.Error(fmt.Sprintf("/images/slug handler failed to update image permissions: %v", err))
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	// audit log
	var changes []any

	if existing.Title != updated.Title {
		changes = append(changes,
			slog.String("previous_title", existing.Title),
			slog.String("new_title", updated.Title))
	}

	if existing.Description != updated.Description {
		changes = append(changes,
			slog.String("previous_description", existing.Description),
			slog.String("new_description", updated.Description))
	}

	if existing.ImageDate != updated.ImageDate {
		changes = append(changes,
			slog.String("previous_image_date", existing.ImageDate),
			slog.String("new_image_date", updated.ImageDate))
	}

	if existing.ObjectKey != updated.ObjectKey {
		changes = append(changes,
			slog.String("previous_object_key", existing.ObjectKey),
			slog.String("new_object_key", updated.ObjectKey))
	}

	if existing.IsArchived != updated.IsArchived {
		changes = append(changes,
			slog.Bool("previous_is_archived", existing.IsArchived),
			slog.Bool("new_is_archived", updated.IsArchived))
	}

	if existing.IsPublished != updated.IsPublished {
		changes = append(changes,
			slog.Bool("previous_is_published", existing.IsPublished),
			slog.Bool("new_is_published", updated.IsPublished))
	}

	if len(changes) > 0 {
		log = log.With(changes...)
		log.Info(fmt.Sprintf("successfully updated image slug %s", existing.Slug))
	}

	w.WriteHeader(http.StatusNoContent) // 204 No Content
}

// handleAddImageRecord handles the POST request for adding an image record.
// It intakes and validates the incoming data, then processes it to create a new image record in the database.
// It also generates a signed URL for the image in object storage to return to the client for uploading the image file.
// This mechanism is based on the file processsing pipeline pattern.
func (h *imageHandler) handleAddImageRecord(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(writeImagesAllowed, svcToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(writeImagesAllowed, accessToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// decode the request body into the AddMetaDataCmd struct
	var cmd api.AddMetaDataCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode request body", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the incoming data
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate create image metadata command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// build placeholder image record waiting for the image file to be processed on
	// generate a pre-signed PUT URL to return for browser to submit the file to object storage
	placeholder, err := h.svc.BuildPlaceholder(cmd)
	if err != nil {
		log.Error("failed to build placeholder image record", "err", err.Error())
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	// adding album associations is optional at this juncture, so do not wait to return
	go func() {
		// add the pre-added album mappings from the upload command.
		albumIds, err := h.getValidAlbumIds(ctx, authedUser.Claims.Subject, cmd.Albums)
		if err != nil {
			log.Error("failed to get valid image slugs", "err", err.Error())
			return // do not block the response, just log the error
		}

		if len(albumIds) > 0 {
			for _, albumId := range albumIds {

				go func(albId, imgId string) {
					// add the image to the album by updating xref table
					if err := h.albums.InsertAlbumImageXref(albId, imgId); err != nil {
						log.Error(fmt.Sprintf("failed to add image to album '%s'", albId), "err", err.Error())
						return
					}

					log.Info(fmt.Sprintf("created xref between image '%s' to album '%s'", imgId, albId))
				}(albumId, placeholder.Id)
			}
		}
	}()

	// adding permissions is optional at this juncture, so do not wait to return
	go func() {
		// get all permissions
		psMap, _, err := h.perms.GetAllPermissions()
		if err != nil {
			log.Error("failed to retrieve all permissions", "err", err.Error())
			return // do not block the response, just log the error
		}

		// get permission ids to add to the image_permissions table
		var permissionIds []string
		for _, p := range cmd.Permissions {
			if perm, ok := psMap[p.Slug]; ok {
				permissionIds = append(permissionIds, perm.Id)
			} else {
				log.Error(fmt.Sprintf("permission slug '%s' not found for user", p.Slug))
			}
		}

		for _, permissionId := range permissionIds {

			go func(imgId, permId string) {
				// add the image to the permissions by updating xref table
				if err := h.svc.InsertImagePermissionXref(imgId, permId); err != nil {
					log.Error(fmt.Sprintf("failed to add image to permission '%s'", permId), "err", err.Error())
					return
				}
				log.Info(fmt.Sprintf("created xref between image '%s' to permission '%s'", imgId, permId))
			}(placeholder.Id, permissionId)
		}
	}()

	log.Info(fmt.Sprintf("successfully created placeholder image record with slug %s", placeholder.Slug))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(placeholder); err != nil {
		log.Error("failed to json encode placeholder image data to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to json encode placeholder image data to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// getValidAlbumIds is a helper function that retrieves the valid album IDs from the provided album command data.
func (h *imageHandler) getValidAlbumIds(ctx context.Context, username string, albumsCmd []api.Album) ([]string, error) {

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve %s's permissions: %v", username, err)
	}

	// get the user's allowed albums
	allowed, _, err := h.albums.GetAllowedAlbums(ps)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve %s's allowed albums: %v", username, err)
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
