package crypt

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
)

// AlbumImageCryptor is an interface which defines methods for encrypting and decrypting
type AlbumImageCryptor interface {

	// DecryptAlbumImages decrypts sensitive fields in an AlbumImageRecord model.
	DecryptAlbumImage(r *db.AlbumImageRecord) error
}

// NewAlbumImageCryptor creates a new AlbumImageCryptor instance, returning a pointer to the concrete implementation.
func NewAlbumImageCryptor(c data.Cryptor) AlbumImageCryptor {
	return &albumImageCryptor{
		cryptor: c,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackageService)).
			With(slog.String(util.ComponentKey, util.ComponentAlbumImageCryptor)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ AlbumImageCryptor = (*albumImageCryptor)(nil)

// albumImageCryptor is the concrete implementation of the AlbumImageCryptor interface, which
// is a wrapper around the data.Cryptor interface from carapace, providing
// album image-specific fields encryption and decryption methods.
type albumImageCryptor struct {
	cryptor data.Cryptor

	logger *slog.Logger
}

// DecryptAlbumImage is a wrapper function around the generalized
// field decryption function, decryptAlbumFields,
// that decrypts the sensitive fields in the album image record struct.
func (aic *albumImageCryptor) DecryptAlbumImage(r *db.AlbumImageRecord) error {
	if r == nil {
		return errors.New("album image record cannot be nil")
	}

	return aic.decryptAlbumImageFields(
		&r.AlbumTitle,
		&r.AlbumDescription,
		&r.AlbumSlug,
		&r.ImageTitle,
		&r.ImageDescription,
		&r.FileName,
		&r.ImageDate,
		&r.ObjectKey,
		&r.ImageSlug,
	)
}

// decryptAlbumImageFields is a helper function which decrypts the provided fields
// using the provided Cryptor instance. It takes pointers to strings to modify them in place.
func (aic *albumImageCryptor) decryptAlbumImageFields(
	albumTitle,
	albumDescription,
	albumSlug,
	imageTitle,
	imageDescription,
	fileName,
	imageDate,
	objectKey,
	imageSlug *string,
) error {

	var (
		wg sync.WaitGroup

		albumTitleCh = make(chan string, 1)
		albumDescCh  = make(chan string, 1)
		albumSlugCh  = make(chan string, 1)

		imageTitleCh = make(chan string, 1)
		imageDescCh  = make(chan string, 1)
		fileNameCh   = make(chan string, 1)
		imageDateCh  = make(chan string, 1)
		objectKeyCh  = make(chan string, 1)
		imageSlugCh  = make(chan string, 1)

		errChan = make(chan error, 9) // buffer size equal to number of fields to avoid goroutine leaks
	)

	wg.Add(3)
	go aic.decrypt(*albumTitle, "album title", albumTitleCh, errChan, &wg)
	go aic.decrypt(*albumDescription, "album description", albumDescCh, errChan, &wg)
	go aic.decrypt(*albumSlug, "album slug", albumSlugCh, errChan, &wg)

	wg.Add(5)
	go aic.decrypt(*imageTitle, "image title", imageTitleCh, errChan, &wg)
	go aic.decrypt(*imageDescription, "image description", imageDescCh, errChan, &wg)
	go aic.decrypt(*fileName, "file name", fileNameCh, errChan, &wg)
	go aic.decrypt(*objectKey, "object key", objectKeyCh, errChan, &wg)
	go aic.decrypt(*imageSlug, "image slug", imageSlugCh, errChan, &wg)

	if *imageDate != "" {
		wg.Add(1)
		go aic.decrypt(*imageDate, "image date", imageDateCh, errChan, &wg)
	}

	wg.Wait()
	close(albumTitleCh)
	close(albumDescCh)
	close(albumSlugCh)
	close(imageTitleCh)
	close(imageDescCh)
	close(fileNameCh)
	close(imageDateCh)
	close(objectKeyCh)
	close(imageSlugCh)
	close(errChan)

	// check for errors during decryption
	if len(errChan) > 0 {
		var errs []error
		for e := range errChan {
			errs = append(errs, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("failed to decrypt album image record: %v", errors.Join(errs...))
		}
	}

	// set the decrypted fields in the album image record
	if albumTitle != nil {
		if title, ok := <-albumTitleCh; ok {
			*albumTitle = title
		}
	}

	if albumDescription != nil {
		if desc, ok := <-albumDescCh; ok {
			*albumDescription = desc
		}
	}

	if albumSlug != nil {
		if slug, ok := <-albumSlugCh; ok {
			*albumSlug = slug
		}
	}

	if imageTitle != nil {
		if title, ok := <-imageTitleCh; ok {
			*imageTitle = title
		}
	}

	if imageDescription != nil {
		if desc, ok := <-imageDescCh; ok {
			*imageDescription = desc
		}
	}

	if fileName != nil {
		if fn, ok := <-fileNameCh; ok {
			*fileName = fn
		}
	}

	if imageDate != nil {
		if id, ok := <-imageDateCh; ok {
			*imageDate = id
		}
	}

	if objectKey != nil {
		if ok, ok2 := <-objectKeyCh; ok2 {
			*objectKey = ok
		}
	}

	if imageSlug != nil {
		if slug, ok := <-imageSlugCh; ok {
			*imageSlug = slug
		}
	}

	return nil
}

// decrypt is a helper function to decrypt a single field and send the result to the appropriate channel.
func (aic *albumImageCryptor) decrypt(ciphertext, fieldname string, resultCh chan<- string, errCh chan<- error, wg *sync.WaitGroup) {

	defer wg.Done()

	if ciphertext == "" {
		errCh <- fmt.Errorf("failed to decrypt '%s' field because it is empty", fieldname)
		return
	}

	// decrypt the field
	plaintext, err := aic.cryptor.DecryptServiceData(ciphertext)
	if err != nil {
		aic.logger.Error(fmt.Sprintf("failed to decrypt %s '%s': %v", fieldname, ciphertext, err))
		errCh <- err
		return
	}

	// send the plaintext to the channel
	resultCh <- string(plaintext)
}
