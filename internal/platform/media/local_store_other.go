//go:build !unix

package media

import (
	"context"
	"errors"
	"io"
)

// Verified staging already relies on Unix unlink semantics. Do not silently
// substitute path-following local storage on unsupported deployment platforms.
var errLocalPlatform = errors.New("local media storage requires a Unix filesystem boundary")

func (s *LocalStore) PutStream(context.Context, string, io.ReadSeeker, int64, string, string) error {
	return errLocalPlatform
}
func (s *LocalStore) Stat(context.Context, string) (ObjectInfo, error) {
	return ObjectInfo{}, errLocalPlatform
}
func (s *LocalStore) Open(context.Context, string, *ByteRange) (Object, error) {
	return Object{}, errLocalPlatform
}
func (s *LocalStore) Delete(context.Context, string) error { return errLocalPlatform }
