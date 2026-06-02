//go:build !cgo

package flakerelease

import (
	"errors"
	"io"
)

type lzmaWriter struct{}

func newLZMAWriter(io.Writer) (*lzmaWriter, error) {
	return nil, errors.New("liblzma compression requires cgo")
}

func (*lzmaWriter) Write([]byte) (int, error) {
	return 0, errors.New("liblzma compression requires cgo")
}

func (*lzmaWriter) Close() error {
	return errors.New("liblzma compression requires cgo")
}
