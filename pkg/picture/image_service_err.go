package picture

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/pixie/internal/util"
)

// ImageServiceErr is an interface that consoldiates error handling in the image service.
type ImageServiceErr interface {

	// HandleImageServiceError handles errors that occur in the image service.
	HandleImageServiceError(err error, w http.ResponseWriter)
}

// NewImageServiceErr creates a new ImageServiceErr instance.
func NewImageServiceErr() ImageServiceErr {
	return &imageServiceErr{
		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePicture)).
			With(slog.String(util.ComponentKey, util.ComponentImageServiceErr)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ ImageServiceErr = (*imageServiceErr)(nil)

type imageServiceErr struct {
	logger *slog.Logger
}

// HandleImageServiceError handles errors that occur in the image service.
func (e *imageServiceErr) HandleImageServiceError(err error, w http.ResponseWriter) {

	// for safety, handle param err == nil
	// this should never happen, but if it does, log it and return 500
	if err == nil {
		e.logger.Error("HandleImageServiceError was invoked, but submitted error was nil")
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    "Internal Server Error",
		}
		e.SendJsonErr(w)
		return
	}

	switch {
	case strings.Contains(err.Error(), "no access") ||
		strings.Contains(err.Error(), "not have permission") ||
		strings.Contains(err.Error(), "not have correct permission") ||
		strings.Contains(err.Error(), "published"):
		e := connect.ErrorHttp{
			StatusCode: http.StatusForbidden,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return

	case strings.Contains(err.Error(), "not found") ||
		strings.Contains(err.Error(), "does not exist") ||
		strings.Contains(err.Error(), "no albums found") ||
		strings.Contains(err.Error(), "no permissions found"):
		e := connect.ErrorHttp{
			StatusCode: http.StatusNotFound,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return

	case strings.Contains(err.Error(), "is archived"):
		e := connect.ErrorHttp{
			StatusCode: http.StatusGone,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return

	case strings.Contains(err.Error(), "not well-formed") ||
		strings.Contains(err.Error(), "invalid") ||
		strings.Contains(err.Error(), "not valid") ||
		strings.Contains(err.Error(), "not a valid") ||
		strings.Contains(err.Error(), "must be") ||
		strings.Contains(err.Error(), "required"):
		e := connect.ErrorHttp{
			StatusCode: http.StatusUnprocessableEntity,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return

	default:
		e := connect.ErrorHttp{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
		e.SendJsonErr(w)
		return
	}
}
