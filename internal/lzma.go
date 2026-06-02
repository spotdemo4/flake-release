//go:build cgo

package flakerelease

/*
#cgo pkg-config: liblzma
#include <lzma.h>
#include <stdlib.h>
#include <string.h>

static lzma_stream *lzma_stream_new(void) {
	lzma_stream *stream = calloc(1, sizeof(lzma_stream));
	if (stream == NULL) {
		return NULL;
	}
	lzma_stream init = LZMA_STREAM_INIT;
	*stream = init;
	return stream;
}

static lzma_ret lzma_easy_encoder_go(lzma_stream *stream, uint32_t preset) {
	return lzma_easy_encoder(stream, preset, LZMA_CHECK_CRC64);
}

static lzma_ret lzma_code_go(lzma_stream *stream, lzma_action action) {
	return lzma_code(stream, action);
}

static void lzma_end_go(lzma_stream *stream) {
	lzma_end(stream);
	free(stream);
}

static void lzma_set_in(lzma_stream *stream, const uint8_t *next_in, size_t avail_in) {
	stream->next_in = next_in;
	stream->avail_in = avail_in;
}

static void lzma_set_out(lzma_stream *stream, uint8_t *next_out, size_t avail_out) {
	stream->next_out = next_out;
	stream->avail_out = avail_out;
}

static size_t lzma_avail_in(lzma_stream *stream) {
	return stream->avail_in;
}

static size_t lzma_avail_out(lzma_stream *stream) {
	return stream->avail_out;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"unsafe"
)

const lzmaBufferSize = 128 * 1024

type lzmaWriter struct {
	out     io.Writer
	stream  *C.lzma_stream
	inBuf   unsafe.Pointer
	outBuf  unsafe.Pointer
	closed  bool
	cleaned bool
}

func newLZMAWriter(out io.Writer) (*lzmaWriter, error) {
	writer := &lzmaWriter{
		out: out,
	}

	writer.stream = C.lzma_stream_new()
	if writer.stream == nil {
		return nil, errors.New("allocating lzma stream")
	}

	writer.inBuf = C.malloc(C.size_t(lzmaBufferSize))
	if writer.inBuf == nil {
		writer.cleanup()
		return nil, errors.New("allocating lzma input buffer")
	}

	writer.outBuf = C.malloc(C.size_t(lzmaBufferSize))
	if writer.outBuf == nil {
		writer.cleanup()
		return nil, errors.New("allocating lzma output buffer")
	}

	preset := C.uint32_t(9) | C.uint32_t(C.LZMA_PRESET_EXTREME)
	if ret := C.lzma_easy_encoder_go(writer.stream, preset); ret != C.LZMA_OK {
		writer.cleanup()
		return nil, lzmaError(ret)
	}

	return writer, nil
}

func (w *lzmaWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("write to closed lzma writer")
	}

	written := 0
	for len(p) > 0 {
		chunk := min(len(p), lzmaBufferSize)

		C.memcpy(w.inBuf, unsafe.Pointer(&p[0]), C.size_t(chunk))
		C.lzma_set_in(w.stream, (*C.uint8_t)(w.inBuf), C.size_t(chunk))
		if err := w.code(C.LZMA_RUN); err != nil {
			C.lzma_set_in(w.stream, nil, 0)
			return written, err
		}
		C.lzma_set_in(w.stream, nil, 0)

		written += chunk
		p = p[chunk:]
	}

	return written, nil
}

func (w *lzmaWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.cleanup()

	C.lzma_set_in(w.stream, nil, 0)
	return w.code(C.LZMA_FINISH)
}

func (w *lzmaWriter) code(action C.lzma_action) error {
	for {
		C.lzma_set_out(w.stream, (*C.uint8_t)(w.outBuf), C.size_t(lzmaBufferSize))
		ret := C.lzma_code_go(w.stream, action)
		available := C.lzma_avail_out(w.stream)
		produced := lzmaBufferSize - int(available)

		if produced > 0 {
			data := C.GoBytes(w.outBuf, C.int(produced))
			if _, err := w.out.Write(data); err != nil {
				return err
			}
		}

		switch ret {
		case C.LZMA_OK:
			if action == C.LZMA_RUN && C.lzma_avail_in(w.stream) == 0 {
				return nil
			}
		case C.LZMA_STREAM_END:
			return nil
		default:
			return lzmaError(ret)
		}
	}
}

func (w *lzmaWriter) cleanup() {
	if w.cleaned {
		return
	}
	w.cleaned = true

	if w.stream != nil {
		C.lzma_end_go(w.stream)
		w.stream = nil
	}
	if w.inBuf != nil {
		C.free(w.inBuf)
		w.inBuf = nil
	}
	if w.outBuf != nil {
		C.free(w.outBuf)
		w.outBuf = nil
	}
}

func lzmaError(ret C.lzma_ret) error {
	switch ret {
	case C.LZMA_MEM_ERROR:
		return errors.New("lzma memory allocation failed")
	case C.LZMA_OPTIONS_ERROR:
		return errors.New("unsupported lzma compression options")
	case C.LZMA_UNSUPPORTED_CHECK:
		return errors.New("unsupported lzma integrity check")
	case C.LZMA_PROG_ERROR:
		return errors.New("lzma programmer error")
	default:
		return fmt.Errorf("lzma error: %d", int(ret))
	}
}
