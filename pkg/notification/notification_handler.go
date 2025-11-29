package notification

import (
	"context"
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

	// get telemetry from request
	tel := connect.ObtainTelemetry(r, h.logger)
	log := h.logger.With(tel.TelemetryFields()...)

	// add telemetry to context for downstream calls + service functions
	ctx := context.WithValue(r.Context(), connect.TelemetryKey, tel)

	// validate method
	if r.Method != http.MethodPost {
		log.Error(fmt.Sprintf("unsupported method %s for endpoint %s", r.Method, r.URL.Path))
		e := connect.ErrorHttp{
			StatusCode: http.StatusMethodNotAllowed,
			Message:    fmt.Sprintf("unsupported method %s for endpoint %s", r.Method, r.URL.Path),
		}
		e.SendJsonErr(w)
	}

	// validate s2s token
	s2sToken := r.Header.Get("Service-Authorization")
	authedSvc, err := h.s2s.BuildAuthorized(requiredScopes, s2sToken)
	if err != nil {
		log.Error("failed to validate s2s token", "err", err.Error())
		connect.RespondAuthFailure(connect.S2s, err, w)
		return
	}
	log = log.With("requesting_service", authedSvc.Claims.Subject)

	// pat validation call to s2s service
	pat := r.Header.Get("Authorization")

	// clip "Bearer " prefix if present
	if len(pat) > 7 && pat[0:7] == "Bearer " {
		pat = pat[7:]
	}

	// validate the PAT
	authedPat, err := h.pat.BuildAuthorized(ctx, requiredScopes, pat)
	if err != nil {
		log.Error("failed to validate PAT", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnauthorized,
			Message:    "failed to validate PAT",
		}
		e.SendJsonErr(w)
		return
	}
	log = log.With("actor", authedPat.ServiceName)

	// decode the request body
	var webhook storage.WebhookPutObject
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		log.Error("failed to decode webhook payload", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to decode webhook payload",
		}
		e.SendJsonErr(w)
		return
	}

	// validate the webhook payload
	if err := webhook.Validate(); err != nil {
		log.Error("failed to validate webhook payload", "err", err.Error())
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}

	log.Info(fmt.Sprintf("received image upload notification for object %s in bucket %s", webhook.MinioKey, webhook.Records[0].S3.Bucket.Name))

	// send webhook to processing queue
	h.queue <- webhook

	// respond with 200 OK right away
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}
