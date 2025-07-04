package permissions

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	exo "github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/pixie/internal/util"
)

var (
	readPermissionsAllowed  = []string{"r:pixie:permissions:*"}
	writePermissionsAllowed = []string{"w:pixie:permissions:*"}
)

// Handler defines the methods for interacting with the /pms/permissions endpoint.
type Handler interface {
	// GetPermissions handles requests against the /permissions endpoint.
	HandlePermissions(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new permissions handler and provides a pointer to a concrete implementation.
func NewHandler(s Service, s2s, iam jwt.Verifier) Handler {
	return &permissionsHandler{
		service: s,
		s2s:     s2s,
		iam:     iam,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.ComponentKey, util.ComponentPermissions)).
			With(slog.String(util.PackageKey, util.PackagePermissions)),
	}
}

var _ Handler = (*permissionsHandler)(nil)

// permissionsHandler implements the Handler interface for managing permissions to gallery data models and images.
type permissionsHandler struct {
	service Service
	s2s     jwt.Verifier
	iam     jwt.Verifier

	logger *slog.Logger
}

// HandlePermissions implements the HandlePremissions method that handles requests against the /permissions endpoint.
func (h *permissionsHandler) HandlePermissions(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		// Handle GET request to retrieve all permissions
		h.getAllPermissions(w, r)
		return
	case http.MethodPost:
		// Handle POST request to create a new permission (not implemented in this example)
		h.createPermission(w, r)
		return
	default:
		// Handle unsupported methods
		h.logger.Warn(fmt.Sprintf("Unsupported method %s for %s", r.Method, r.URL.Path))
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    "Method not allowed",
		}
		e.SendJsonErr(w)
		return
	}
}

// getAllPermissions retrieves all permissions from the service and sends them as a JSON response.
func (h *permissionsHandler) getAllPermissions(w http.ResponseWriter, r *http.Request) {

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(readPermissionsAllowed, s2sToken); err != nil {
		h.logger.Error(fmt.Sprintf("/permissions endpoint failed authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	if _, err := h.iam.BuildAuthorized(readPermissionsAllowed, iamToken); err != nil {
		h.logger.Error(fmt.Sprintf("/permissions endpoint failed authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// no need for fine-grained permissions here at this time

	// retrieve all permissions from the service
	permissions, err := h.service.GetAllPermissions()
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve permissions: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// respond with the permissions as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(permissions); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode permissions response: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode permissions response",
		}
		e.SendJsonErr(w)
		return
	}
}

// createPermission is a helper method which handles the creation of a new permission
func (h *permissionsHandler) createPermission(w http.ResponseWriter, r *http.Request) {

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(writePermissionsAllowed, s2sToken); err != nil {
		h.logger.Error(fmt.Sprintf("/permissions endpoint failed authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authrorized, err := h.iam.BuildAuthorized(writePermissionsAllowed, iamToken)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/permissions endpoint failed authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// get the request body
	var cmd exo.Permission
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error(fmt.Sprintf("failed to decode request body: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "invalid request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the permission command
	if err := cmd.Validate(); err != nil {
		h.logger.Error(fmt.Sprintf("Failed to validate permission command: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("invalid permission command: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// validate service is correct
	if strings.ToLower(strings.TrimSpace(cmd.Service)) != util.ServiceGallery {
		h.logger.Error(fmt.Sprintf("Invalid service '%s' in permission command", cmd.Service))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    "invalid service in add-permission command",
		}
		e.SendJsonErr(w)
		return
	}

	// build premission record
	p := &Permission{
		Name:        strings.TrimSpace(cmd.Name),
		Service:     strings.ToLower(strings.TrimSpace(cmd.Service)),
		Description: cmd.Description,
		Active:      cmd.Active,
	}

	// create the permission in the service
	record, err := h.service.CreatePermission(p)
	if err != nil {
		h.logger.Error(fmt.Sprintf("Failed to create permission: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to create permission",
		}
		e.SendJsonErr(w)
		return
	}

	// audit record
	h.logger.Info(fmt.Sprintf("Permission '%s - %s' created by %s", record.Id, record.Name, authrorized.Claims.Subject))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(record); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode permission response: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode permission response",
		}
		e.SendJsonErr(w)
		return
	}
}
