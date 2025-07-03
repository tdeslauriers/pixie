package permissions

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
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
		h.logger.Error(fmt.Sprintf("Failed to retrieve permissions: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	// respond with the permissions as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(permissions); err != nil {
		h.logger.Error(fmt.Sprintf("Failed to encode permissions response: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to encode permissions response",
		}
		e.SendJsonErr(w)
		return
	}
}
