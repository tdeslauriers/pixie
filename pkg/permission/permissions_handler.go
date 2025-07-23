package permission

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
	readPermissionsAllowed  = []string{"r:pixie:*", "r:pixie:permissions:*"}
	writePermissionsAllowed = []string{"w:pixie:*", "w:pixie:permissions:*"}
)

// Handler defines the methods for interacting with the /pms/permissions endpoint.
type Handler interface {
	// GetPermissions handles requests against the /permissions endpoint.
	HandlePermissions(w http.ResponseWriter, r *http.Request)

	// HandlePermission handles requests against the /permissions/{slug} endpoint.
	HandlePermission(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new permissions handler and provides a pointer to a concrete implementation.
func NewHandler(s exo.Service, s2s, iam jwt.Verifier) Handler {
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
	service exo.Service
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

// HandlePermission implements the HandlePermission method that handles requests against the /permissions/{slug} endpoint.
func (h *permissionsHandler) HandlePermission(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		h.getPermissionBySlug(w, r)
		return
	case http.MethodPost:
		// Handle PUT (acutally a post) request to update an existing permission (not implemented in this example)
		h.updatePermission(w, r)
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
	_, permissions, err := h.service.GetAllPermissions()
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

// getPermissionBySlug is a helper method which retrieves a permission by its slug and sends it as a JSON response.
func (h *permissionsHandler) getPermissionBySlug(w http.ResponseWriter, r *http.Request) {

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

	// extract the slug from the URL path
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to extract slug from request: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "invalid slug in request",
		}
		e.SendJsonErr(w)
		return
	}

	// retrieve the permission by slug from the service
	permission, err := h.service.GetPermissionBySlug(slug)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve permission by slug '%s': %v", slug, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to retrieve permission by slug '%s': %v", slug, err),
		}
		e.SendJsonErr(w)
		return
	}

	// respond with the permission as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(permission); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode permission response: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode permission response",
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
	if strings.ToLower(strings.TrimSpace(cmd.ServiceName)) != util.ServiceGallery {
		h.logger.Error(fmt.Sprintf("Invalid service '%s' in permission command", cmd.ServiceName))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    "invalid service in add-permission command",
		}
		e.SendJsonErr(w)
		return
	}

	// build premission record
	p := &exo.PermissionRecord{
		ServiceName: strings.ToLower(strings.TrimSpace(cmd.ServiceName)),
		Permission:  strings.ToUpper(strings.TrimSpace(cmd.Permission)),
		Name:        strings.TrimSpace(cmd.Name),
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

// updatePermission is a helper method which handles the update of an existing permission
func (h *permissionsHandler) updatePermission(w http.ResponseWriter, r *http.Request) {

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(writePermissionsAllowed, s2sToken); err != nil {
		h.logger.Error(fmt.Sprintf("/permissions/{slug} endpoint failed authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authrorized, err := h.iam.BuildAuthorized(writePermissionsAllowed, iamToken)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/permissions/{slug} endpoint failed authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	// get the request body
	var cmd exo.PermissionRecord
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
		h.logger.Error(fmt.Sprintf("failed to validate permission command: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("invalid permission command: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// validate service is correct.  This check is unnecessary since the value in the cmd is
	// dropped, but it is a good practice to ensure the service is correct since a mismatch would
	// indicate tampering of the request.
	if strings.ToLower(strings.TrimSpace(cmd.ServiceName)) != util.ServiceGallery {
		h.logger.Error(fmt.Sprintf("invalid service '%s' in permission command", cmd.ServiceName))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    "invalid service in update-permission command",
		}
		e.SendJsonErr(w)
		return
	}

	// look up the existing permission by slug
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to extract slug from request: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "invalid slug in request",
		}
		e.SendJsonErr(w)
		return
	}

	record, err := h.service.GetPermissionBySlug(slug)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to retrieve permission by slug '%s': %v", slug, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to retrieve permission by slug '%s': %v", slug, err),
		}
		e.SendJsonErr(w)
		return
	}

	updated := &exo.PermissionRecord{
		Id:          record.Id,
		ServiceName: record.ServiceName,                                 // drop the service value from the cmd, cannot be updated
		Permission:  strings.ToUpper(strings.TrimSpace(cmd.Permission)), // ensure permission is uppercase
		Name:        strings.TrimSpace(cmd.Name),
		Description: cmd.Description,
		Active:      cmd.Active,
		CreatedAt:   record.CreatedAt,
		Slug:        record.Slug, // keep the existing slug
		// slug index should not be returned in the response
	}

	// update the permission in the service
	if err := h.service.UpdatePermission(updated); err != nil {
		h.logger.Error(fmt.Sprintf("failed to update permission '%s': %v", record.Id, err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to update permission '%s': %v", record.Id, err),
		}
		e.SendJsonErr(w)
		return
	}

	// audit record
	if updated.Permission != record.Permission {
		h.logger.Info(fmt.Sprintf("permission record's permission field updated from '%s' to '%s' by %s", record.Permission, updated.Permission, authrorized.Claims.Subject))
	}

	if updated.Name != record.Name {
		h.logger.Info(fmt.Sprintf("permission name field updated from '%s' to '%s' by %s", record.Name, updated.Name, authrorized.Claims.Subject))
	}

	if updated.Description != record.Description {
		h.logger.Info(fmt.Sprintf("permission description field updated from '%s' to '%s' by %s", record.Description, updated.Description, authrorized.Claims.Subject))
	}

	if updated.Active != record.Active {
		h.logger.Info(fmt.Sprintf("permission active field updated from '%t' to '%t' by %s", record.Active, updated.Active, authrorized.Claims.Subject))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(updated); err != nil {
		h.logger.Error(fmt.Sprintf("failed to json encode updated permission response: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to json encode updated permission response",
		}
		e.SendJsonErr(w)
		return
	}
}
