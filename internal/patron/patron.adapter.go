package patron

import (
	"database/sql"
	"errors"

	"github.com/tdeslauriers/carapace/pkg/data"
)

// PatronRepository is the interface for data functions related to patrons.
type PatronRepository interface {

	// FindByIndex retrieves a patron record by the user index from the database.
	FindByIndex(index string) (*PatronRecord, error)

	// InsertPatron inserts a new patron record into the database.
	InsertPatron(patron PatronRecord) error
}

// NewPatronRepository creates a new PatronRepository instance, returning a pointer to the concrete implementation.
func NewPatronRepository(db *sql.DB) PatronRepository {
	return &patronRepository{
		db: db,
	}
}

var _ PatronRepository = (*patronRepository)(nil)

// patronRepository is the concrete implementation of the PatronRepository interface.
type patronRepository struct {
	db *sql.DB
}

// FindByIndex is the concrete implementation of the interface method which
// retrieves a patron record by the user index from the database.
func (r *patronRepository) FindByIndex(index string) (*PatronRecord, error) {

	qry := `
	SELECT 
		u.uuid,
		u.username,
		u.user_index,
		u.slug,
		u.slug_index,
		u.created_at,
		u.updated_at,
		u.is_archived,
		u.is_active
	FROM patron u
	WHERE u.user_index = ?`

	p, err := data.SelectOneRecord[PatronRecord](r.db, qry, index)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("username not found") // -> index not found
		} else {
			return nil, err
		}
	}

	return &p, nil
}

// InsertPatron is the concrete implementation of the interface method which
// inserts a new patron record into the database.
func (r *patronRepository) InsertPatron(patron PatronRecord) error {

	qry := `
		INSERT INTO patron (
			uuid,
			username,
			user_index,
			slug,
			slug_index,
			created_at,
			updated_at,
			is_archived,
			is_active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return data.InsertRecord(r.db, qry, patron)
}
