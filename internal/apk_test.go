package flakerelease

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type testAPKSignature int

const (
	testAPKUnsigned testAPKSignature = iota
	testAPKSignedV1
	testAPKSignedV2
)

// writeTestAPK writes a zip with an AndroidManifest.xml and extra empty entries, signed
// with a v1 signature file or an APK Signing Block holding no signatures.
func writeTestAPK(t *testing.T, path string, signature testAPKSignature, extraEntries int) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := []string{"AndroidManifest.xml"}
	if signature == testAPKSignedV1 {
		names = append(names, "META-INF/MANIFEST.MF", "META-INF/CERT.SF", "META-INF/CERT.RSA")
	}
	for i := range extraEntries {
		names = append(names, fmt.Sprintf("res/%d", i))
	}
	for _, name := range names {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	if signature == testAPKSignedV2 {
		data = insertTestAPKSigningBlock(t, data)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// insertTestAPKSigningBlock inserts an APK Signing Block before the central directory and
// moves the offsets that point past it.
func insertTestAPKSigningBlock(t *testing.T, data []byte) []byte {
	t.Helper()
	pair := binary.LittleEndian.AppendUint64(nil, 4)
	pair = binary.LittleEndian.AppendUint32(pair, 0x7109871a)
	blockSize := uint64(len(pair) + 8 + len(apkSigningBlockMagic))
	block := binary.LittleEndian.AppendUint64(nil, blockSize)
	block = append(block, pair...)
	block = binary.LittleEndian.AppendUint64(block, blockSize)
	block = append(block, apkSigningBlockMagic...)

	eocd, err := findZipEndOfCentralDirectory(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	shift := uint64(len(block))
	centralDirectory := int64(binary.LittleEndian.Uint32(data[eocd+16:]))
	if centralDirectory == 0xffffffff {
		locator := eocd - zip64EndOfCentralDirectoryLocatorSize
		record := int64(binary.LittleEndian.Uint64(data[locator+8:]))
		centralDirectory = int64(binary.LittleEndian.Uint64(data[record+zip64EndOfCentralDirectoryCDOffsetOffset:]))
		binary.LittleEndian.PutUint64(data[locator+8:], uint64(record)+shift)
		binary.LittleEndian.PutUint64(data[record+zip64EndOfCentralDirectoryCDOffsetOffset:], uint64(centralDirectory)+shift)
	} else {
		binary.LittleEndian.PutUint32(data[eocd+16:], uint32(uint64(centralDirectory)+shift))
	}

	out := make([]byte, 0, len(data)+len(block))
	out = append(out, data[:centralDirectory]...)
	out = append(out, block...)
	return append(out, data[centralDirectory:]...)
}

func TestAPKSigned(t *testing.T) {
	for _, test := range []struct {
		name         string
		signature    testAPKSignature
		extraEntries int
		want         bool
	}{
		{name: "unsigned", signature: testAPKUnsigned, want: false},
		{name: "v1", signature: testAPKSignedV1, want: true},
		{name: "v2", signature: testAPKSignedV2, want: true},
		{name: "zip64 unsigned", signature: testAPKUnsigned, extraEntries: 0xffff, want: false},
		{name: "zip64 v2", signature: testAPKSignedV2, extraEntries: 0xffff, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			apk := filepath.Join(t.TempDir(), "app.apk")
			writeTestAPK(t, apk, test.signature, test.extraEntries)
			got, err := apkSigned(apk)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("apkSigned() = %t; want %t", got, test.want)
			}
		})
	}
}

func TestAPKSignedIgnoresSignatureFilesOutsideMETAINF(t *testing.T) {
	apk := filepath.Join(t.TempDir(), "app.apk")
	file, err := os.Create(apk)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range []string{"AndroidManifest.xml", "assets/CERT.SF", "META-INF/sub/CERT.SF"} {
		if _, err := writer.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	signed, err := apkSigned(apk)
	if err != nil {
		t.Fatal(err)
	}
	if signed {
		t.Fatal("apkSigned() = true; want false")
	}
}

func TestAPKSignedRejectsNonZip(t *testing.T) {
	apk := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(apk, []byte("not a zip file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := apkSigned(apk); err == nil {
		t.Fatal("apkSigned() returned no error for a non-zip file")
	}
}
