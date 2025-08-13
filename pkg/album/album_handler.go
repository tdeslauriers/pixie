package album

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/pixie/internal/util"
)

// scopes required to interact with the album handler(s) endpoints
var (
	readAlbumAllowed = []string{"r:pixie:*", "r:pixie:albums:*"}
	editAlbumAllowed = []string{"w:pixie:*", "w:pixie:albums:*"}
)

// Handler is an interface that defines methods for handling album-related requests.
type Handler interface {

	// HandleAlbum handles requests related to a specific album.
	HandleAlbum(w http.ResponseWriter, r *http.Request)

	// HandleAlbums handles requests related to albums.
	HandleAlbums(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new Handler instance and returns a pointer to the concrete implementation.
func NewHandler(s Service, s2s, iam jwt.Verifier) Handler {
	return &handler{
		svc: s,

		s2s: s2s,
		iam: iam,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumHandler)).
			With(slog.String(util.PackageKey, util.PackageAlbum)),
	}
}

var _ Handler = (*handler)(nil)

// handler is the concrete implementation of the Handler interface.
type handler struct {
	svc Service
	s2s jwt.Verifier
	iam jwt.Verifier // inherently nil because this will come from registration -> s2s

	logger *slog.Logger
}

// HandleAlbum is the concrete implementation of the interface method which handles album-related requests
// for a specific album.
func (h *handler) HandleAlbum(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		h.handleGetAlbum(w, r)
		return
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
func (h *handler) HandleAlbums(w http.ResponseWriter, r *http.Request) {

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
func (h *handler) handleGetAlbums(w http.ResponseWriter, r *http.Request) {

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

	albums, err := h.svc.GetAllowedAlbums(authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("Failed to retrieve albums for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to retrieve albums",
		}
		e.SendJsonErr(w)
		return
	}

	// send the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(albums); err != nil {
		h.logger.Error(fmt.Sprintf("Failed to encode albums for user '%s': %v", authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to encode albums",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleGetAlbum handles the retrieval of a specific album record.
func (h *handler) handleGetAlbum(w http.ResponseWriter, r *http.Request) {

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
		h.logger.Error(fmt.Sprintf("Failed to extract album slug from request url: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "Failed to extract album slug from request url",
		}
		e.SendJsonErr(w)
		return
	}

	// retrieve the album record
	album, err := h.svc.GetAlbumBySlug(slug, authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("Failed to retrieve album '%s' for user '%s': %v", slug, authorized.Claims.Subject, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to retrieve album",
		}
		e.SendJsonErr(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(album); err != nil {
		h.logger.Error(fmt.Sprintf("Failed to encode album '%s': %v", album.Title, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to encode album",
		}
		e.SendJsonErr(w)
		return
	}
}

// handleUpdateAlbum handles the update of an existing album record.
func (h *handler) handleUpdateAlbum(w http.ResponseWriter, r *http.Request) {

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
		h.logger.Error(fmt.Sprintf("Failed to extract album slug from request url: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "Failed to extract album slug from request url",
		}
		e.SendJsonErr(w)
		return
	}

	// decode the request body into an cmd record
	var cmd UpdateAlbumCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error("Failed to decode album record", slog.Any("error", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "Failed to decode album record",
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

	// update the album record in the database
	updated, err := h.svc.UpdateAlbum(slug, cmd, authorized.Claims.Subject)
	if err != nil {
		h.logger.Error(fmt.Sprintf("Failed to update album record '%s': %v", slug, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to update album record",
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
}

// handleCreateAlbum handles the creation of a new album record.
func (h *handler) handleCreateAlbum(w http.ResponseWriter, r *http.Request) {

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
	var cmd AddAlbumCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error("Failed to decode album record", slog.Any("error", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "Failed to decode album record",
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
		h.logger.Error(fmt.Sprintf("Failed to create album record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to create album record",
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	h.logger.Info(fmt.Sprintf("album record '%s' created by user '%s'", created.Title, authorized.Claims.Subject))

	// build the response
	response := Album{
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
		h.logger.Error(fmt.Sprintf("Failed to encode created album record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to encode created album record",
		}
		e.SendJsonErr(w)
		return
	}
}
