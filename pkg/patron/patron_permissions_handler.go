package patron

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/internal/util"
)

// scopes requried to access /patrons/permissions endpoint
var (
	readPatronPermissionsAllowed   = []string{"r:pixie:*", "r:pixie:patrons:permissions:*"}
	updatePatronPermissionsAllowed = []string{"w:pixie:*", "w:pixie:patrons:permissions:*"}
)

// PermissionHandler is the interface for handling updates to a users permissions.
type PermissionHandler interface {

	// HandlePermissions handles the requests to update a users permissions.
	HandlePermissions(w http.ResponseWriter, r *http.Request)
}

// NewPermissionHandler creates a new PermissionHandler instance, returning a pointer to the concrete implementation.
func NewPermissionHandler(s Service, s2s, iam jwt.Verifier) PermissionHandler {
	return &permissionHandler{
		service:     s,
		s2sVerifier: s2s,
		iamVerifier: iam,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePatron)).
			With(slog.String(util.ComponentKey, util.ComponentPatron)),
	}
}

var _ PermissionHandler = (*permissionHandler)(nil)

// permissionHandler is the concrete implementation of the PermissionHandler interface.
// It handles requests to update a user's permissions.
type permissionHandler struct {
	service     Service
	s2sVerifier jwt.Verifier
	iamVerifier jwt.Verifier

	logger *slog.Logger
}

// HandlePermissions is the concrete implementation of the interface method which
// handles the request to update a user's permissions.
func (h *permissionHandler) HandlePermissions(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		h.getPatronPermissions(w, r)
		return
	case http.MethodPost:
		h.updatePatronPermissions(w, r)
		return
	default:
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

// getPatronPermissions retrieves a patron's permissions from the database.
func (h *permissionHandler) getPatronPermissions(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate the s2s token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2sVerifier.BuildAuthorized(readPatronPermissionsAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iamVerifier.BuildAuthorized(readPatronPermissionsAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	username := r.URL.Query().Get("username")
	if username == "" {
		log.Error("username missing from query params", "err", "username query parameter is required")
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "username query parameter is required",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the username
	if err := validate.IsValidEmail(username); err != nil {
		log.Error("failed to validate username", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// get the patron's permissions
	_, ps, err := h.service.GetPatronPermissions(ctx, username)
	if err != nil {
		log.Error("failed to retrieve permissions for patron", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions for patron",
		}
		e.SendJsonErr(w)
		return
	}

	// respond with the permissions
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ps); err != nil {
		log.Error("failed to encode permissions for patron to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode permissions for patron to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// update PatronPermissions updates a patron's permissions via xref in the database.
func (h *permissionHandler) updatePatronPermissions(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate the s2s token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2sVerifier.BuildAuthorized(updatePatronPermissionsAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iamVerifier.BuildAuthorized(updatePatronPermissionsAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// get the request body
	var cmd permissions.UpdatePermissionsCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode request body", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the request
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate update permissions command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// look up the patron by email
	p, err := h.service.GetByUsername(ctx, cmd.Entity) // entity will be username in this case
	if err != nil {
		log.Error(fmt.Sprintf("failed to retrieve patron by email '%s'", cmd.Entity), "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusNotFound,
			Message:    fmt.Sprintf("failed to retrieve patron by email '%s'", cmd.Entity),
		}
		e.SendJsonErr(w)
		return
	}

	// update the patron's permissions
	added, removed, err := h.service.UpdatePatronPermissions(ctx, p, cmd.Permissions)
	if err != nil {
		log.Error(fmt.Sprintf("failed to update permissions for patron '%s'", p.Username), "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to update permissions for patron '%s'", p.Username),
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	if len(added) <= 0 && len(removed) <= 0 {
		log.Warn(fmt.Sprintf("update command fired successfully, but no permission changes for patron '%s'", p.Username))
	}

	if len(added) > 0 {
		for _, permission := range added {
			log.Info(fmt.Sprintf("permission '%s' added to patron '%s'", permission.Name, p.Username))
		}
	}

	if len(removed) > 0 {
		for _, permission := range removed {
			log.Info(fmt.Sprintf("permission '%s' removed from patron '%s'", permission.Name, p.Username))
		}
	}

	// respond 204: No Content
	w.WriteHeader(http.StatusNoContent)
}
