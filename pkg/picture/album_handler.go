package picture

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
func (h *albumHandler) HandleAlbums(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:

		// get slug from path if exists
		slug := r.PathValue("slug")
		switch slug {
		case "":
			h.handleGetAlbums(w, r)
			return
		case "staged":
			h.handleGetStagedImages(w, r)
			return
		default:
			h.handleGetAlbum(w, r)
			return
		}
	case http.MethodPost:
		h.handleCreateAlbum(w, r)
		return
	case http.MethodPut:
		h.handleUpdateAlbum(w, r)
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

// handleActiveAlbums handles the retrieval of all album records a user has permission to view.
func (h *albumHandler) handleGetAlbums(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(readAlbumAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(readAlbumAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(ctx, authedUser.Claims.Subject)
	if err != nil {
		log.Error("failed to retrieve permissions for user", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	_, albums, err := h.svc.GetAllowedAlbumsData(ctx, ps)
	if err != nil {
		log.Error("failed to retrieve albums", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve albums",
		}
		e.SendJsonErr(w)
		return
	}

	log.Info(fmt.Sprintf("successfully retrieved %d albums", len(albums)))

	// send the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(albums); err != nil {
		log.Error("failed to encode albums to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode albums to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleGetAlbum handles the retrieval of a specific album record.
func (h *albumHandler) handleGetAlbum(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(readAlbumAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(readAlbumAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// extract the slug from the request URL
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		log.Error("failed to extract album slug from request url", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(ctx, authedUser.Claims.Subject)
	if err != nil {
		log.Error("failed to retrieve permissions for user", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// retrieve the album record
	album, err := h.svc.GetAlbumBySlug(ctx, slug, ps)
	if err != nil {
		log.Error(fmt.Sprintf("failed to retrieve album '%s' for user", slug), "err", err.Error())
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	log.Info(fmt.Sprintf("successfully retrieved album '%s'", album.Title))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(album); err != nil {
		log.Error("failed to encode album to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode album to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleGetStagedImages handles the retrieval of staged images.
// Note, for the sake of simplicity, staged is treated like an album, even though it is not one.
// There is not database record for staged, it is simply a collection of images that have not been assigned to an album.
func (h *albumHandler) handleGetStagedImages(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// Note: this endpoint is read only, so only read permissions are required
	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(readAlbumAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(readAlbumAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	// this is admin endpoint, so add actor to logger
	log = log.With("actor", authedUser.Claims.Subject)

	// do not need to extract slug since it is always "staged"

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(ctx, authedUser.Claims.Subject)
	if err != nil {
		log.Error("failed to retrieve permissions for user", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve user's gallery permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the user has the curator permission
	if _, ok := ps["CURATOR"]; !ok {
		log.Error("user does not have permission to view staged images")
		e := connect.ErrorHttp{
			StatusCode: http.StatusForbidden,
			Message:    "You do not have permission to view staged images",
		}
		e.SendJsonErr(w)
		return
	}

	album, err := h.svc.GetStagedImages(ctx)
	if err != nil {
		log.Error("failed to retrieve staged images album", "err", err.Error())
		h.svc.HandleImageServiceError(ctx, err, w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(album); err != nil {
		log.Error("failed to encode staged images album to json", "err", err.Error())
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

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(editAlbumAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(editAlbumAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// extract the slug from the request URL
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		log.Error("failed to extract album slug from request url", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// decode the request body into an cmd record
	var cmd api.AlbumUpdateCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode update album command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode update album command",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the album record
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate update album command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// get the user's permissions
	ps, _, err := h.perms.GetPatronPermissions(ctx, authedUser.Claims.Subject)
	if err != nil {
		log.Error(fmt.Sprintf("failed to retrieve permissions for user '%s': %v", authedUser.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// lookup the existing album record to ensure it exists
	existing, err := h.svc.GetAlbumBySlug(ctx, slug, ps)
	if err != nil {
		log.Error("failed to retrieve existing album", "err", err.Error())
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
		log.Error("failed to update album record", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	var changes []any

	if updated.Title != existing.Title {
		changes = append(changes,
			slog.String("previous_title", existing.Title),
			slog.String("updated_title", updated.Title))
	}

	if updated.Description != existing.Description {
		changes = append(changes,
			slog.String("previous_description", existing.Description),
			slog.String("updated_description", updated.Description))
	}

	if updated.IsArchived != existing.IsArchived {
		changes = append(changes,
			slog.Bool("previous_is_archived", existing.IsArchived),
			slog.Bool("updated_is_archived", updated.IsArchived))
	}

	if len(changes) > 0 {
		log = log.With(changes...)
		log.Info(fmt.Sprintf("successfully updated album slug %s", slug))
	} else {
		log.Warn(fmt.Sprintf("update command executed, but no changes were made to album slug %s", slug))
	}

	// respond 204 No Content
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateAlbum handles the creation of a new album record.
func (h *albumHandler) handleCreateAlbum(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// validate service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(editAlbumAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(editAlbumAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// decode the request body into an cmd record
	var cmd api.AddAlbumCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode create album command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode create album command",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the album record
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate create album command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// create the album record in the database
	// which will populate the missing fields
	created, err := h.svc.CreateAlbum(cmd)
	if err != nil {
		log.Error("failed to create album record", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to create album record",
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	log.Info(fmt.Sprintf("successfully created album '%s' with slug '%s'", created.Title, created.Slug))

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
		log.Error("failed to encode created album record to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode created album record to json",
		}
		e.SendJsonErr(w)
		return
	}
}
