package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestChecksumFor(t *testing.T) {
	want := sha256.Sum256([]byte("archive"))
	data := []byte(hex.EncodeToString(want[:]) + "  raph_linux_amd64.tar.gz\n")

	got, err := checksumFor(data, "raph_linux_amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("checksum = %x, want %x", got, want)
	}
}

func TestExtractZip(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("raph.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractZip(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("binary = %q, want binary", got)
	}
}

func TestExtractTarGzip(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	body := []byte("binary")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "raph", Size: int64(len(body)), Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractTarGzip(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("binary = %q, want binary", got)
	}
}
