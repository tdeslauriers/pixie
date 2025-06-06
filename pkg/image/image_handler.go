package image

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
	readImagesAllowed  = []string{"r:pixie:image:*"}
	writeImagesAllowed = []string{"w:pixie:image:*"}
)

// Handler is the interface for image processing service handlers.
// It defines the methods that any image handler must implement to process image requests.
type Handler interface {

	// HandleImage handles the image processing request
	HandleImage(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new image handler instance, returning a pointer to the concrete implementation.
func NewHandler(s Service, s2s, iam jwt.Verifier) Handler {
	return &imageHandler{
		svc: s,
		s2s: s2s,
		iam: iam,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackageImage)).
			With(slog.String(util.ComponentKey, util.ComponentImage)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ Handler = (*imageHandler)(nil)

// imageHandler is a concrete implementation of the Handler interface.
type imageHandler struct {
	svc Service // The image service instance to handle image processing tasks
	s2s jwt.Verifier
	iam jwt.Verifier

	logger *slog.Logger
}

// HandleImage is the concrete implementation of the HandleImage method.
func (h *imageHandler) HandleImage(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		// Handle GET request for image processing
		h.getImageData(w, r)
		return
	default:
		errMsg := fmt.Sprintf("unsupported method %s for image handler", r.Method)
		h.logger.Error(errMsg)
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    errMsg,
		}
		e.SendJsonErr(w)
	}
}

// getImageData handles the GET request for image processing.
func (h *imageHandler) getImageData(w http.ResponseWriter, r *http.Request) {

	// validate s2s token
	svcToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(readImagesAllowed, svcToken); err != nil {
		h.logger.Error(fmt.Sprintf("/template/slug handler failed to authorize service token: %v", err))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// validate iam token
	accessToken := r.Header.Get("Authorization")
	if _, err := h.iam.BuildAuthorized(readImagesAllowed, accessToken); err != nil {
		h.logger.Error(fmt.Sprintf("/template/slug handler failed to authorize iam token: %v", err))
		connect.RespondAuthFailure(connect.User, err, w)
		return
	}

	slug, err := connect.GetValidSlug(r)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/template/slug handler failed to get valid slug: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to get valid slug",
		}
		e.SendJsonErr(w)
		return
	}

	imageData, err := h.svc.GetImageData(slug)
	if err != nil {
		h.logger.Error(fmt.Sprintf("/image/slug handler failed to get image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to get image data",
		}
		e.SendJsonErr(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(imageData); err != nil {
		h.logger.Error(fmt.Sprintf("/image/slug handler failed to encode image data: %v", err))
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to encode image data",
		}
		e.SendJsonErr(w)
		return
	}
}
