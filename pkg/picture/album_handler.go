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

// scopes required to interact with the album handler(s) endpoints
var (
	readAlbumAllowed = []string{"r:pixie:*", "r:pixie:albums:*"}
	editAlbumAllowed = []string{"w:pixie:*", "w:pixie:albums:*"}
)

// Handler is an interface that defines methods for handling album-related requests.
type AlbumHandler interface {

	// HandleAlbum handles requests related to a specific album.
	HandleAlbum(w http.ResponseWriter, r *http.Request)

	// HandleAlbums handles requests related to albums.
	HandleAlbums(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new Handler instance and returns a pointer to the concrete implementation.
func NewAlbumHandler(s Service, p permission.Service, s2s, iam jwt.Verifier) AlbumHandler {
	return &albumHandler{
		svc:   s,
		perms: p,
		s2s:   s2s,
		iam:   iam,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumHandler)).
			With(slog.String(util.PackageKey, util.PackagePicture)),
	}
}

var _ AlbumHandler = (*albumHandler)(nil)

// handler is the concrete implementation of the Handler interface.
type albumHandler struct {
	svc   Service
	perms permission.Service
	s2s   jwt.Verifier
	iam   jwt.Verifier // inherently nil because this will come from registration -> s2s

	logger *slog.Logger
}

// HandleAlbum is the concrete implementation of the interface method which handles album-related requests
// for a specific album.
func (h *albumHandler) HandleAlbum(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:

		// get the slug to determine if user is going to /albums/staged or /albums/{slug}
		// get the url slug from the request
		segments := strings.Split(r.URL.Path, "/")

		var slug string
		if len(segments) > 1 {
			slug = segments[len(segments)-1]
		} else {
			h.logger.Error("slug is required for GET request to /albums/{slug}")
			e := connect.ErrorHttp{
				StatusCode: http.StatusBadRequest,
				Message:    "slug is required for GET request to /albums/{slug}",
			}
			e.SendJsonErr(w)
			return
		}

		if slug == "staged" {

			h.handleGetStagedImages(w, r)
			return
		} else {

			h.handleGetAlbum(w, r)
			return
		}

	case http.MethodPost:
		h.handleUpdateAlbum(w, r)
		return
	// case http.MethodDelete:
	// 	h.handleDeleteAlbum(w, r)
	// 	return
	default:
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    "Method not allowed",
		}
		e.SendJsonErr(w)
		return
	}
}

// HandleAlbums is the concrete implementation of the interface method which handles album-related requests.
// It will handle the request logic, including validation and persistence of album records.
func (h *albumHandler) HandleAlbums(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		h.handleGetAlbums(w, r)
		return
	case http.MethodPost:
		h.handleCreateAlbum(w, r)
		return
	default:
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    "Method not allowed",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleActiveAlbums handles the retrieval of all album records a user has permission to view.
func (h *albumHandler) handleGetAlbums(w http.ResponseWriter, r *http.Request) {

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(readAlbumAllowed, s2sToken); err != nil {
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authorized, err := h.iam.BuildAuthorized(readAlbumAllowed, iamToken)
	if err != nil {
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve permissions for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	_, albums, err := h.svc.GetAllowedAlbumsData(ps)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve albums for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve albums",
		}
		e.SendJsonErr(w)
		return
	}

	// send the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(albums); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode albums for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode albums",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleGetAlbum handles the retrieval of a specific album record.
func (h *albumHandler) handleGetAlbum(w http.ResponseWriter, r *http.Request) {

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(readAlbumAllowed, s2sToken); err != nil {
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authorized, err := h.iam.BuildAuthorized(readAlbumAllowed, iamToken)
	if err != nil {
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// extract the slug from the request URL
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to extract album slug from request url: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to extract album slug from request url",
		}
		e.SendJsonErr(w)
		return
	}

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve permissions for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// retrieve the album record
	album, err := h.svc.GetAlbumBySlug(slug, ps)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve album '%s' for user '%s': %v", slug, authorized.Claims.Subject, err))
		h.svc.HandleImageServiceError(err, w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(album); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode album '%s': %v", album.Title, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode album",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleGetStagedImages handles the retrieval of staged images.
// Note, for the sake of simplicity, staged is treated like an album, even though it is not one.
// There is not database record for staged, it is simply a collection of images that have not been assigned to an album.
func (h *albumHandler) handleGetStagedImages(w http.ResponseWriter, r *http.Request) {

	// validate service token
	// this endpoint is read-only, so only read scope is required
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(readAlbumAllowed, s2sToken); err != nil {
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	// this endpoint is read-only, so only read scope is required
	// However, the user also needs the curator permission to view staged images
	iamToken := r.Header.Get("Authorization")
	authorized, err := h.iam.BuildAuthorized(append(readAlbumAllowed, "r:pixie:curator"), iamToken)
	if err != nil {
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// do not need to extract slug since it is always "staged"

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve gallery permissions for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve gallery permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the user has the curator permission
	if _, ok := ps["CURATOR"]; !ok {
		h.logger.Error(fmt.Sprintf("User '%s' does not have the curator permission to view staged images", authorized.Claims.Subject))
		e := connect.ErrorHttp{
			StatusCode: http.StatusForbidden,
			Message:    "You do not have permission to view staged images",
		}
		e.SendJsonErr(w)
		return
	}

	album, err := h.svc.GetStagedImages()
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve staged images for user '%s': %v", authorized.Claims.Subject, err))
		h.svc.HandleImageServiceError(err, w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(album); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode staged images album: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode staged images album",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleUpdateAlbum handles the update of an existing album record.
func (h *albumHandler) handleUpdateAlbum(w http.ResponseWriter, r *http.Request) {

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(editAlbumAllowed, s2sToken); err != nil {
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authorized, err := h.iam.BuildAuthorized(editAlbumAllowed, iamToken)
	if err != nil {
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// extract the slug from the request URL
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to extract album slug from request url: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to extract album slug from request url",
		}
		e.SendJsonErr(w)
		return
	}

	// decode the request body into an cmd record
	var cmd api.AlbumUpdateCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error("failed to decode album record", slog.Any("error", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode album record",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the album record
	if err := cmd.Validate(); err != nil {
		h.logger.Error("Album record validation failed", slog.Any("error", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    "Album record validation failed",
		}
		e.SendJsonErr(w)
		return
	}

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve permissions for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// lookup the existing album record to ensure it exists
	existing, err := h.svc.GetAlbumBySlug(slug, ps)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve existing album '%s' for user '%s': %v", slug, authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve existing album",
		}
		e.SendJsonErr(w)
		return
	}

	// build the updated album record
	// only certain fields can be updated; other fields are immutable and will be ignored
	updated := db.AlbumRecord{
		Id:          existing.Id, // immutable
		Title:       cmd.Title,
		Description: cmd.Description,
		IsArchived:  cmd.IsArchived,
		Slug:        existing.Slug, // slug is immutable -> needed to get blind index
		UpdatedAt:   data.CustomTime{Time: time.Now().UTC()},
	}

	// update the album record in the database
	if err := h.svc.UpdateAlbum(updated); err != nil {
		h.logger.Error(fmt.Sprintf("failed to update album record '%s': %v", slug, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to update album record",
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	if updated.Title != existing.Title {
		h.logger.Info(fmt.Sprintf("album record title '%s' updated to '%s' by user '%s'", existing.Title, updated.Title, authorized.Claims.Subject))
	}

	if updated.Description != existing.Description {
		h.logger.Info(fmt.Sprintf("album record description '%s' updated to '%s' by user '%s'", existing.Description, updated.Description, authorized.Claims.Subject))
	}

	if updated.IsArchived != existing.IsArchived {
		h.logger.Info(fmt.Sprintf("album record is_archived status changed to '%t' by user '%s'", updated.IsArchived, authorized.Claims.Subject))
	}

	// respond 204 No Content
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateAlbum handles the creation of a new album record.
func (h *albumHandler) handleCreateAlbum(w http.ResponseWriter, r *http.Request) {

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(editAlbumAllowed, s2sToken); err != nil {
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authorized, err := h.iam.BuildAuthorized(editAlbumAllowed, iamToken)
	if err != nil {
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// decode the request body into an cmd record
	var cmd api.AddAlbumCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error("failed to decode album record", slog.Any("error", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode album record",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the album record
	if err := cmd.Validate(); err != nil {
		h.logger.Error("Album record validation failed", slog.Any("error", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    "Album record validation failed",
		}
		e.SendJsonErr(w)
		return
	}

	// create the album record in the database
	// which will populate the missing fields
	created, err := h.svc.CreateAlbum(cmd)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to create album record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to create album record",
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	h.logger.Info(fmt.Sprintf("album record '%s' created by user '%s'", created.Title, authorized.Claims.Subject))

	// build the response
	response := api.Album{
		Id:          created.Id,
		Title:       created.Title,
		Description: created.Description,
		Slug:        created.Slug,
		CreatedAt:   created.CreatedAt,
		UpdatedAt:   created.UpdatedAt,
		IsArchived:  created.IsArchived,
		// cover image url omitted since will not exist yet
	}

	// respond with the created album record
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode created album record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode created album record",
		}
		e.SendJsonErr(w)
		return
	}
}
