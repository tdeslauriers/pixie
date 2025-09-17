package crypt

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// AlbumCryptor is an interface which defines methods for encrypting and decrypting
type AlbumCryptor interface {

	// EncryptAlbumRecord encrypts sensitive fields in the AlbumRecord struct.
	EncryptAlbumRecord(a *db.AlbumRecord) error

	// EncryptAlbum encrypts sensitive fields in the Album struct.
	EncryptAlbum(a *api.Album) error

	// DecryptAlbumRecord decrypts sensitive fields in the AlbumRecord struct.
	DecryptAlbumRecord(a *db.AlbumRecord) error

	// DecryptAlbum decrypts sensitive fields in the Album struct.
	DecryptAlbum(a *api.Album) error
}

// NewAlbumCryptor creates a new AlbumCryptor instance, returning a pointer to the concrete implementation.
func NewAlbumCryptor(c data.Cryptor) AlbumCryptor {
	return &albumCryptor{
		cryptor: c,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackageService)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumCryptor)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ AlbumCryptor = (*albumCryptor)(nil)

// albumCryptor is the concrete implementation of the AlbumCryptor interface, which
// is a wrapper around the data.Cryptor interface from carapace, providing
// album-specific fields encryption and decryption methods.
type albumCryptor struct {
	cryptor data.Cryptor

	logger *slog.Logger
}

// EncryptAlbumRecord is a wrapper function around the generalized
// field encryption function, encryptAlbumFields,
// that encrypts the sensitive fields in the album record struct.
func (ac *albumCryptor) EncryptAlbumRecord(a *db.AlbumRecord) error {
	if a == nil {
		return fmt.Errorf("album record cannot be nil")
	}

	// encrypt the sensitive fields in the album record
	return ac.encryptAlbumFields(&a.Title, &a.Description, &a.Slug)
}

// EncryptAlbum is a wrapper funciton arournd the generalized
// field encryption function, encryptAlbumFields, which
// encrypts sensitive fields in the Album struct.
func (ac *albumCryptor) EncryptAlbum(a *api.Album) error {
	if a == nil {
		return fmt.Errorf("album cannot be nil")
	}

	// encrypt the sensitive fields in the album struct
	return ac.encryptAlbumFields(&a.Title, &a.Description, &a.Slug)
}

// EncryptAlbumFields encrypts the sensitive fields in the album record.
func (ac *albumCryptor) encryptAlbumFields(
	titlePtr *string,
	descriptionPtr *string,
	slugPtr *string,
) error {

	if titlePtr == nil || descriptionPtr == nil || slugPtr == nil {
		return fmt.Errorf("album record fields cannot be nil")
	}

	var (
		wg sync.WaitGroup

		titleCh       = make(chan string, 1)
		descriptionCh = make(chan string, 1)
		slugCh        = make(chan string, 1)

		errCh = make(chan error, 3)
	)

	wg.Add(3)
	go ac.encrypt("album title", *titlePtr, titleCh, errCh, &wg)
	go ac.encrypt("album description", *descriptionPtr, descriptionCh, errCh, &wg)
	go ac.encrypt("album slug", *slugPtr, slugCh, errCh, &wg)

	wg.Wait()
	close(titleCh)
	close(descriptionCh)
	close(slugCh)
	close(errCh)

	// check for errors during encryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to encrypt album record: %v", errors.Join(errs...))
		}
	}

	// set the encrypted fields in the album record
	if titlePtr != nil {
		if title, ok := <-titleCh; ok {
			*titlePtr = title
		}
	}

	if descriptionPtr != nil {
		if desc, ok := <-descriptionCh; ok {
			*descriptionPtr = desc
		}
	}

	if slugPtr != nil {
		if slug, ok := <-slugCh; ok {
			*slugPtr = slug
		}
	}

	return nil
}

// encrypt is a helper method that encrypts a field in the album record.
func (ac *albumCryptor) encrypt(field, plaintext string, fieldCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	if plaintext == "" {
		errCh <- fmt.Errorf("field '%s' cannot be empty", field)
		return
	}

	// encrypt the field
	encrypted, err := ac.cryptor.EncryptServiceData([]byte(plaintext))
	if err != nil {
		errCh <- fmt.Errorf("failed to encrypt '%s' field '%s': %v", field, plaintext, err)
		return
	}

	fieldCh <- encrypted
}

// decryptAlbumRecord is a wrapper function for decrypting sensitive fields in the AlbumRecord struct.
func (ac *albumCryptor) DecryptAlbumRecord(a *db.AlbumRecord) error {
	if a == nil {
		return fmt.Errorf("album record cannot be nil")
	}
	// decrypt the sensitive fields in the album record
	return ac.decryptAlbumFields(
		&a.Title,
		&a.Description,
		&a.Slug,
	)
}

// decryptAlbum is a wrapper function which decrypts the sensitive fields in the Album struct.
func (ac *albumCryptor) DecryptAlbum(a *api.Album) error {
	if a == nil {
		return fmt.Errorf("album cannot be nil")
	}
	// decrypt the sensitive fields in the album struct
	return ac.decryptAlbumFields(
		&a.Title,
		&a.Description,
		&a.Slug,
	)
}

// decryptAlbumFields decrypts the sensitive fields in the album record.
func (ac *albumCryptor) decryptAlbumFields(
	titlePtr *string,
	descPtr *string,
	slugPtr *string,
) error {

	var (
		wg      sync.WaitGroup
		titleCh = make(chan string, 1)
		descCh  = make(chan string, 1)
		slugCh  = make(chan string, 1)
		errCh   = make(chan error, 3)
	)

	wg.Add(3)
	go ac.decrypt("album title", *titlePtr, titleCh, errCh, &wg)
	go ac.decrypt("album description", *descPtr, descCh, errCh, &wg)
	go ac.decrypt("album slug", *slugPtr, slugCh, errCh, &wg)

	wg.Wait()
	close(titleCh)
	close(descCh)
	close(slugCh)
	close(errCh)

	// check for errors during decryption
	if len(errCh) > 0 {
		var errs []error
		for e := range errCh {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to decrypt album record: %v", errors.Join(errs...))
		}
	}

	// set the decrypted fields in the album record
	*titlePtr = <-titleCh
	*descPtr = <-descCh
	*slugPtr = <-slugCh

	return nil
}

// decrypt is a helper method that decrypts a field in the album record.
func (ac *albumCryptor) decrypt(field, ciphertext string, fieldCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	if ciphertext == "" {
		errCh <- fmt.Errorf("failed to decrypt '%s' field because it is empty", field)
		return
	}

	// decrypt the field
	decrypted, err := ac.cryptor.DecryptServiceData(ciphertext)
	if err != nil {
		errCh <- fmt.Errorf("failed to decrypt '%s' field: %v", field, err)
		return
	}

	fieldCh <- string(decrypted)
}
