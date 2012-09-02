package falib

import "errors"

var (
	ErrAbsoluteDirectoryPath = errors.New("unable to process archive with absolute path reference")
	ErrFileHeaderMismatch    = errors.New("unexpected file header")
	ErrCrcMismatch           = errors.New("crc64 mismatch")
	ErrUnrecognizedBlockType = errors.New("unrecognized block type")
)
