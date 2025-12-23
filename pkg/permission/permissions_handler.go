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
}

// NewHandler creates a new permissions handler and provides a pointer to a concrete implementation.
func NewHandler(s exo.Service, s2s, iam jwt.Verifier) Handler {
	return &permissionsHandler{
		// service: s,
		s2s: s2s,
		iam: iam,

		logger: slog.Default().
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

		// get slug if exists
		slug := r.PathValue("slug")
		if slug == "" {

			// Handle GET request to retrieve all permissions
			h.getAllPermissions(w, r)
			return
		} else {
			// Handle GET request to retrieve a permission by slug
			h.getPermissionBySlug(w, r)
			return
		}
	case http.MethodPost:
		// Handle POST request to create a new permission
		h.createPermission(w, r)
		return
	case http.MethodPut:
		// Handle PUT request to update an existing permission
		h.updatePermission(w, r)
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

// getAllPermissions retrieves all permissions from the service and sends them as a JSON response.
func (h *permissionsHandler) getAllPermissions(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(readPermissionsAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate service token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(readPermissionsAllowed, iamToken)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/permissions endpoint failed authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// no need for fine-grained permissions here at this time

	// retrieve all permissions from the service
	_, permissions, err := h.service.GetAllPermissions()
	if err != nil {
		log.Error("failed to retrieve permissions", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to retrieve permissions",
		}
		e.SendJsonErr(w)
		return
	}

	log.Info(fmt.Sprintf("retrieved %d permissions", len(permissions)))

	// respond with the permissions as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(permissions); err != nil {
		log.Error("failed to encode permissions responseto json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode permissions response to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// getPermissionBySlug is a helper method which retrieves a permission by its slug and sends it as a JSON response.
func (h *permissionsHandler) getPermissionBySlug(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(readPermissionsAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate service token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authedUser, err := h.iam.BuildAuthorized(readPermissionsAllowed, iamToken)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/permissions endpoint failed authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authedUser.Claims.Subject)

	// extract the slug from the URL path
	slug, err := connect.GetValidSlug(r)
	if err != nil {
		log.Error("failed to extract slug from request", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// retrieve the permission by slug from the service
	permission, err := h.service.GetPermissionBySlug(slug)
	if err != nil {
		log.Error("failed to retrieve permission by slug", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	log.Info(fmt.Sprintf("successfully retrieved permission '%s' by slug '%s'", permission.Name, slug))

	// respond with the permission as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(permission); err != nil {
		log.Error("failed to encode permission response to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode permission response to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// createPermission is a helper method which handles the creation of a new permission
func (h *permissionsHandler) createPermission(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(writePermissionsAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate service token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authrorized, err := h.iam.BuildAuthorized(writePermissionsAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authrorized.Claims.Subject)

	// get the request body
	var cmd exo.Permission
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode request body", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the permission command
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate create permission command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// validate service is correct
	if strings.ToLower(strings.TrimSpace(cmd.ServiceName)) != util.ServiceGallery {
		log.Error(fmt.Sprintf("Invalid service '%s' in permission command", cmd.ServiceName))
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
		log.Error("failed to create permission", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to create permission",
		}
		e.SendJsonErr(w)
		return
	}

	// audit record
	log.Info(fmt.Sprintf("successfully created permission '%s'", record.Name))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(record); err != nil {
		log.Error("failed to encode created permission response to json", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode created permission response to json",
		}
		e.SendJsonErr(w)
		return
	}
}

// updatePermission is a helper method which handles the update of an existing permission
func (h *permissionsHandler) updatePermission(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// validate the service token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(writePermissionsAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate service token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// validate the iam token
	iamToken := r.Header.Get("Authorization")
	authrorized, err := h.iam.BuildAuthorized(writePermissionsAllowed, iamToken)
	if err != nil {
		log.Error("failed to validate iam token", "err", err.Error())
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}
	log = log.With("actor", authrorized.Claims.Subject)

	// get the request body
	var cmd exo.PermissionRecord
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode request body", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the permission command
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate update permission command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// validate service is correct.  This check is unnecessary since the value in the cmd is
	// dropped, but it is a good practice to ensure the service is correct since a mismatch would
	// indicate tampering of the request.
	if strings.ToLower(strings.TrimSpace(cmd.ServiceName)) != util.ServiceGallery {
		log.Error(fmt.Sprintf("invalid service '%s' in permission command", cmd.ServiceName))
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
		log.Error("failed to extract slug from request", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	record, err := h.service.GetPermissionBySlug(slug)
	if err != nil {
		log.Error("failed to retrieve permission by slug", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
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
		log.Error("failed to update permission", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// audit log
	var updatedFields []any

	if updated.Permission != record.Permission {
		updatedFields = append(updatedFields,
			slog.String("previous_permission", record.Permission),
			slog.String("updated_permission", updated.Permission))
	}

	if updated.Name != record.Name {
		updatedFields = append(updatedFields,
			slog.String("previous_name", record.Name),
			slog.String("updated_name", updated.Name))
	}

	if updated.Description != record.Description {
		updatedFields = append(updatedFields,
			slog.String("previous_description", record.Description),
			slog.String("updated_description", updated.Description))
	}

	if updated.Active != record.Active {
		updatedFields = append(updatedFields,
			slog.Bool("previous_active", record.Active),
			slog.Bool("updated_active", updated.Active))
	}

	if len(updatedFields) > 0 {
		log = log.With(updatedFields...)
		log.Info(fmt.Sprintf("successfully updated permission %s", updated.Slug))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(updated); err != nil {
		log.Error("failed to json encode updated permission response", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to json encode updated permission response",
		}
		e.SendJsonErr(w)
		return
	}
}
