package patron

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/permissions"
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
			With(slog.String(util.ComponentKey, util.ComponentPatron)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
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
	case http.MethodPost:
		h.updatePatronPermissions(w, r)
		return
	default:
		h.logger.Error(fmt.Sprintf("unsupported method %s for patrons permissions handler", r.Method))
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    fmt.Sprintf("unsupported method %s for patrons permissions handler", r.Method),
		}
		e.SendJsonErr(w)
		return
	}
}

// update PatronPermissions updates a patron's permissions via xref in the database.
func (h *permissionHandler) updatePatronPermissions(w http.ResponseWriter, r *http.Request) {

	// validate the s2s token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2sVerifier.BuildAuthorized(updatePatronPermissionsAllowed, s2sToken); err != nil {
		h.logger.Error(fmt.Sprintf("patron permissions endpoint failed to verify service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authorized, err := h.iamVerifier.BuildAuthorized(updatePatronPermissionsAllowed, iamToken)
	if err != nil {
		h.logger.Error(fmt.Sprintf("patron permissions endpoint failed to verify iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// get the request body
	var cmd permissions.UpdatePermissionsCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error(fmt.Sprintf("failed to decode request body: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// validate the request
	if err := cmd.Validate(); err != nil {
		h.logger.Error(fmt.Sprintf("failed to validate request: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// look up the patron by email
	p, err := h.service.GetByUsername(cmd.Entity) // entity will be username in this case
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve patron by email '%s': %v", cmd.Entity, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusNotFound,
			Message:    fmt.Sprintf("failed to retrieve patron by email '%s': %v", cmd.Entity, err),
		}
		e.SendJsonErr(w)
		return
	}

	// update the patron's permissions
	added, removed, err := h.service.UpdatePatronPermissions(p, cmd.Permissions)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to update permissions for patron '%s': %v", p.Username, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to update permissions for patron '%s': %v", p.Username, err),
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	if added != nil && len(added) > 0 {
		for _, permission := range added {
			h.logger.Info(fmt.Sprintf("permission '%s' added to patron '%s' by %s", permission.Name, p.Username, authorized.Claims.Subject))
		}
	}

	if removed != nil && len(removed) > 0 {
		for _, permission := range removed {
			h.logger.Info(fmt.Sprintf("permission '%s' removed from patron '%s' by %s", permission.Name, p.Username, authorized.Claims.Subject))
		}
	}

	// respond 204: No Content
	w.WriteHeader(http.StatusNoContent)
}
