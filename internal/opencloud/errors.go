package opencloud

import (
	"errors"
	"fmt"
)

// The classes of failure a caller can tell apart with errors.Is.
var (
	// ErrRefused is OpenCloud turning the user and app token down.
	ErrRefused = errors.New("token refused")
	// ErrSpaceNotFound is a space the user does not see.
	ErrSpaceNotFound = errors.New("space not found")
	// ErrNotFound is a file or folder that is not in the space.
	ErrNotFound = errors.New("not found")
	// ErrNotAFolder is a file where a folder is asked for.
	ErrNotAFolder = errors.New("not a folder")
	// ErrNotAFile is a folder where a file is asked for.
	ErrNotAFile = errors.New("not a file")
	// ErrDenied is something the user may not do there, like writing as a viewer.
	ErrDenied = errors.New("not permitted")
	// ErrExists is a path that is taken where nothing is to be overwritten.
	ErrExists = errors.New("already there")
	// ErrNoPreview is a file OpenCloud renders no preview of.
	ErrNoPreview = errors.New("no preview")
	// ErrOutsideSpace is a path that leads out of the space.
	ErrOutsideSpace = errors.New("path outside the space")
	// ErrTooLarge is a file past what a read takes.
	ErrTooLarge = errors.New("file too large")
	// ErrBadQuery is a search query OpenCloud's search refuses.
	ErrBadQuery = errors.New("query refused")
	// ErrForeignOrigin is a request to anywhere but the configured OpenCloud.
	ErrForeignOrigin = errors.New("request outside the configured OpenCloud")
)

// classified is an error whose text is the sentence a model reads and whose class is one of the
// errors above.
type classified struct {
	class error
	text  string
}

func (e *classified) Error() string { return e.text }
func (e *classified) Unwrap() error { return e.class }

func fail(class error, format string, args ...any) error {
	return &classified{class: class, text: fmt.Sprintf(format, args...)}
}
