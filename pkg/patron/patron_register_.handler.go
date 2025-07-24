package patron

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
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.PackageKey, util.PackagePatron)).
			With(slog.String(util.ComponentKey, util.ComponentPatronRegister)),
	}
}

var _ PatronRegisterHandler = (*patronRegisterHandler)(nil)

// patronRegisterHandler is the concrete implementation of the PatronRegisterHandler interface.
type patronRegisterHandler struct {
	service     Service
	s2sVerifier jwt.Verifier
	iam         jwt.Verifier // inherently nil because this will come from registration -> s2s endpoint

	logger *slog.Logger
}

// HandleRegister is the concrete implementation of the interface method which handles the registration of a new patron.
// It will handle the registration logic, including validation and persistence of the patron record.
func (h *patronRegisterHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {

	// check the request is a POST request
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// check valid s2s
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2sVerifier.BuildAuthorized(readPatronPermissionsAllowed, s2sToken); err != nil {
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// get the patron registration command from the request body
	var cmd PatronRegisterCmd
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.logger.Error(fmt.Sprintf("failed to decode patron registration command: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    fmt.Sprintf("failed to decode patron registration command: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// validate the command
	if err := cmd.Validate(); err != nil {
		h.logger.Error(fmt.Sprintf("patron registration command validation failed: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    fmt.Sprintf("patron registration command validation failed: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	// create the patron record in the database
	p, err := h.service.CreatePatron(cmd.Username)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to create patron: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to create patron: %v", err),
		}
		e.SendJsonErr(w)
		return
	}

	h.logger.Info(fmt.Sprintf("patron '%s' registered successfully", cmd.Username))

	// respond with the created patron record
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		h.logger.Error(fmt.Sprintf("failed to encode patron record: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    fmt.Sprintf("failed to encode patron record: %v", err),
		}
		e.SendJsonErr(w)
		return
	}
}
