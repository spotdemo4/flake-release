package flakerelease

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	zipEndOfCentralDirectorySignature        = 0x06054b50
	zipEndOfCentralDirectorySize             = 22
	zip64EndOfCentralDirectoryLocatorSig     = 0x07064b50
	zip64EndOfCentralDirectoryLocatorSize    = 20
	zip64EndOfCentralDirectorySignature      = 0x06064b50
	zip64EndOfCentralDirectoryCDOffsetOffset = 48
	apkSigningBlockMagic                     = "APK Sig Block 42"
)

// apkSigned reports whether the APK at path has a v1 (JAR) signature or an APK Signing
// Block, which holds v2 and later signatures.
func apkSigned(apk string) (bool, error) {
	file, err := os.Open(apk)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}

	signed, err := hasAPKSigningBlock(file, info.Size())
	if err != nil {
		return false, fmt.Errorf("reading APK %s: %w", path.Base(apk), err)
	}
	if signed {
		return true, nil
	}

	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return false, fmt.Errorf("reading APK %s: %w", path.Base(apk), err)
	}
	for _, entry := range reader.File {
		dir, name := path.Split(entry.Name)
		if strings.EqualFold(dir, "META-INF/") && strings.EqualFold(path.Ext(name), ".sf") {
			return true, nil
		}
	}
	return false, nil
}

// hasAPKSigningBlock reports whether an APK Signing Block ends where the zip central
// directory starts, which is where Android looks for it.
func hasAPKSigningBlock(reader io.ReaderAt, size int64) (bool, error) {
	eocd, err := findZipEndOfCentralDirectory(reader, size)
	if err != nil {
		return false, err
	}
	record := make([]byte, zipEndOfCentralDirectorySize)
	if _, err := reader.ReadAt(record, eocd); err != nil {
		return false, err
	}
	centralDirectory := int64(binary.LittleEndian.Uint32(record[16:20]))
	if centralDirectory == 0xffffffff {
		centralDirectory, err = zip64CentralDirectoryOffset(reader, eocd)
		if err != nil {
			return false, err
		}
	}
	if centralDirectory < int64(len(apkSigningBlockMagic)) || centralDirectory > eocd {
		return false, nil
	}
	magic := make([]byte, len(apkSigningBlockMagic))
	if _, err := reader.ReadAt(magic, centralDirectory-int64(len(magic))); err != nil {
		return false, err
	}
	return string(magic) == apkSigningBlockMagic, nil
}

// findZipEndOfCentralDirectory returns the offset of the end of central directory record,
// which is followed by a comment of up to 65535 bytes.
func findZipEndOfCentralDirectory(reader io.ReaderAt, size int64) (int64, error) {
	if size < zipEndOfCentralDirectorySize {
		return 0, errors.New("not a zip file")
	}
	start := max(size-zipEndOfCentralDirectorySize-0xffff, 0)
	tail := make([]byte, size-start)
	if _, err := reader.ReadAt(tail, start); err != nil {
		return 0, err
	}
	signature := binary.LittleEndian.AppendUint32(nil, zipEndOfCentralDirectorySignature)
	for i := len(tail) - zipEndOfCentralDirectorySize; i >= 0; i-- {
		if !bytes.Equal(tail[i:i+4], signature) {
			continue
		}
		commentLength := int(binary.LittleEndian.Uint16(tail[i+20 : i+22]))
		if i+zipEndOfCentralDirectorySize+commentLength == len(tail) {
			return start + int64(i), nil
		}
	}
	return 0, errors.New("zip end of central directory record not found")
}

func zip64CentralDirectoryOffset(reader io.ReaderAt, eocd int64) (int64, error) {
	locatorOffset := eocd - zip64EndOfCentralDirectoryLocatorSize
	if locatorOffset < 0 {
		return 0, errors.New("zip64 end of central directory locator not found")
	}
	locator := make([]byte, zip64EndOfCentralDirectoryLocatorSize)
	if _, err := reader.ReadAt(locator, locatorOffset); err != nil {
		return 0, err
	}
	if binary.LittleEndian.Uint32(locator[0:4]) != zip64EndOfCentralDirectoryLocatorSig {
		return 0, errors.New("zip64 end of central directory locator not found")
	}
	recordOffset := int64(binary.LittleEndian.Uint64(locator[8:16]))
	record := make([]byte, zip64EndOfCentralDirectoryCDOffsetOffset+8)
	if _, err := reader.ReadAt(record, recordOffset); err != nil {
		return 0, err
	}
	if binary.LittleEndian.Uint32(record[0:4]) != zip64EndOfCentralDirectorySignature {
		return 0, errors.New("zip64 end of central directory record not found")
	}
	return int64(binary.LittleEndian.Uint64(record[zip64EndOfCentralDirectoryCDOffsetOffset:])), nil
}
