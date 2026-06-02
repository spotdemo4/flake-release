package flakerelease

import (
	"bytes"
	"strings"
	"testing"
)

func TestLZMAWriter(t *testing.T) {
	var out bytes.Buffer
	writer, err := newLZMAWriter(&out)
	if err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("content")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	xzMagic := []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	if !bytes.HasPrefix(out.Bytes(), xzMagic) {
		t.Fatalf("lzma output prefix = %x; want xz magic %x", out.Bytes()[:len(xzMagic)], xzMagic)
	}
}
