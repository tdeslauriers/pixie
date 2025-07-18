package patron

import (
	"log/slog"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/internal/util"
)

// PermissionsService is the interface for managing a patrons permissions.
type PatronService interface {
}

// NewService creates a new Patron service instance, returning a pointer to the concrete implementation.
func NewPatronService(sql data.SqlRepository, i data.Indexer, c data.Cryptor) PatronService {
	return &patronService{
		sql:     sql,
		indexer: i,
		cryptor: c,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackagePatron)).
			With(slog.String(util.ComponentKey, util.ComponentPatron)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ PatronService = (*patronService)(nil)

type patronService struct {
	sql     data.SqlRepository
	indexer data.Indexer
	cryptor data.Cryptor

	logger *slog.Logger
}
