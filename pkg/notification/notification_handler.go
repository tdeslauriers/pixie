package notification

import (
	"encoding/json"

	"fmt"
	"log/slog"
	"net/http"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/pat"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
)

// required scopes for upload notification
var requiredScopes = []string{"w:pixie:*", "w:pixie:images:notify:upload:*"}

// Handler handles external service notification-related operations.
type Handler interface {

	// HandleImageUploadNotification handles notifications of image uploads from
	// object storage via the gateway service.
	HandleImageUploadNotification(w http.ResponseWriter, r *http.Request)
}

// NewHandler creates a new instance of Handler, returning a pointer to the concrete implementation.
func NewHandler(ch chan storage.WebhookPutObject, s2s jwt.Verifier, pat pat.Verifier) Handler {
	return &handler{
		s2s: s2s,
		pat: pat,

		queue: ch,

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.PackageKey, util.PackageNotification)).
			With(slog.String(util.ComponentKey, util.ComponentNotificationHandler)),
	}
}

var _ Handler = (*handler)(nil)

type handler struct {
	s2s jwt.Verifier
	pat pat.Verifier

	queue chan storage.WebhookPutObject

	logger *slog.Logger
}

// HandleImageUploadNotification is a concrete implementation of the Handler interface method which
// handles notifications of image uploads from object storage via the gateway service.
func (h *handler) HandleImageUploadNotification(w http.ResponseWriter, r *http.Request) {

	// validate method
	if r.Method != http.MethodPost {
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    "method not allowed",
		}
		e.SendJsonErr(w)
		return
	}

	// validate s2s token
	s2sToken := r.Header.Get("Service-Authorization")
	if _, err := h.s2s.BuildAuthorized(requiredScopes, s2sToken); err != nil {
		h.logger.Error(fmt.Sprintf("failed to validate s2s token in image upload notification: %v", err.Error()))
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}

	// pat validation call to s2s service
	pat := r.Header.Get("Authorization")

	// clip "Bearer " prefix if present
	if len(pat) > 7 && pat[0:7] == "Bearer " {
		pat = pat[7:]
	}

	// validate the PAT
	_, err := h.pat.BuildAuthorized(requiredScopes, pat)
	if err != nil {
		h.logger.Error(fmt.Sprintf("failed to validate PAT in image upload notification request: %s", err.Error()))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnauthorized,
			Message:    "failed to validate PAT",
		}
		e.SendJsonErr(w)
		return
	}

	// decode the request body
	var webhook storage.WebhookPutObject
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		h.logger.Error(fmt.Sprintf("failed to decode JSON in image upload notification request body: %s", err.Error()))
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "improperly formatted json in webhook request body",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the webhook payload
	if err := webhook.Validate(); err != nil {
		h.logger.Error(fmt.Sprintf("invalid webhook payload in image upload notification request body: %s", err.Error()))
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	h.logger.Info(fmt.Sprintf("received image upload notification for object %s in bucket %s", webhook.MinioKey, webhook.Records[0].S3.Bucket.Name))

	// send webhook to processing queue
	h.queue <- webhook

	// respond with 200 OK right away
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}
