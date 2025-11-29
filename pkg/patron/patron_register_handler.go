package patron

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/pixie/internal/util"
)

var (
	registerPatronAllowed = []string{"w:pixie:*", "w:pixie:s2s:patrons:register:*"}
)

// PatronRegisterHandler is an interface that defines methods for handling patron registration.
type PatronRegisterHandler interface {
	// HandleRegister handles the registration of a new patron.
	HandleRegister(w http.ResponseWriter, r *http.Request)
}

// NewPatronRegisterHandler creates a new PatronRegisterHandler instance and returns a pointer to the concrete implementation.
func NewPatronRegisterHandler(s Service, s2s jwt.Verifier) PatronRegisterHandler {
	return &patronRegisterHandler{
		service:     s,
		s2sVerifier: s2s,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePatron)).
			With(slog.String(util.ComponentKey, util.ComponentPatronRegister)),
	}
}

var _ PatronRegisterHandler = (*patronRegisterHandler)(nil)

// patronRegisterHandler is the concrete implementation of the PatronRegisterHandler interface.
type patronRegisterHandler struct {
	service     Service
	s2sVerifier jwt.Verifier
	// iam         jwt.Verifier // inherently nil because this will come from registration -> s2s endpoint

	logger *slog.Logger
}

// HandleRegister is the concrete implementation of the interface method which handles the registration of a new patron.
// It will handle the registration logic, including validation and persistence of the patron record.
func (h *patronRegisterHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// check the request is a POST request
	if r.Method != http.MethodPost {
		log.Error(fmt.Sprintf("unsupported method %s for endpoint %s", r.Method, r.URL.Path))
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    fmt.Sprintf("unsupported method %s for endpoint %s", r.Method, r.URL.Path),
		}
		e.SendJsonErr(w)
	}

	// check valid s2s
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2sVerifier.BuildAuthorized(registerPatronAllowed, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// get the patron registration command from the request body
	var cmd PatronRegisterCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		log.Error("failed to decode patron registration command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode patron registration command",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the command
	if err := cmd.Validate(); err != nil {
		log.Error("failed to validate patron registration command", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	// check if the patron already exists
	existing, err := h.service.GetByUsername(ctx, strings.TrimSpace(cmd.Username))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			log.Info(fmt.Sprintf("patron with username '%s' does not exist, proceeding with registration", cmd.Username))
		} else {
			log.Error("failed to check if patron already exists", "err", err.Error())
			e := connect.ErrorHttp{
				StatusCode: http.StatusInternalServerError,
				Message:    "failed to check if patron already exists",
			}
			e.SendJsonErr(w)
			return
		}
	}

	if existing != nil {
		h.logger.Error("failed to register patron",
			"err", fmt.Sprintf("patron with username '%s' already exists", cmd.Username))
		e := connect.ErrorHttp{
			StatusCode: http.StatusConflict,
			Message:    fmt.Sprintf("patron with username '%s' already exists", cmd.Username),
		}
		e.SendJsonErr(w)
		return
	}

	// create the patron record in the database
	p, err := h.service.CreatePatron(cmd.Username)
	if err != nil {
		log.Error("failed to create patron", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	log.Info(fmt.Sprintf("patron '%s' registered successfully", cmd.Username))

	// respond with the created patron record
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		log.Error(fmt.Sprintf("failed to encode patron record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to encode patron record: %v", err),
		}
		e.SendJsonErr(w)
		return
	}
}
