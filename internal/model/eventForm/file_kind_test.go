package eventFormModel

import (
	"archive/zip"
	"bytes"
	"testing"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func ole(stream string) []byte {
	return append(append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 64)...), utf16le(stream)...)
}

func tarOf() []byte {
	data := make([]byte, 512)
	copy(data, "notes.txt")
	copy(data[257:], "ustar")
	return data
}

func TestDetectFileKindUsesContent(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		kind FileKind
	}{
		{"cv.pdf", []byte("%PDF-1.7\n"), FileKindPDF},
		{"photo.bin", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), FileKindImage},
		{"photo.jpg", []byte("\xFF\xD8\xFF\xE0\x00\x10JFIF"), FileKindImage},
		{"photo.webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), FileKindImage},
		{"cv.doc", ole("WordDocument"), FileKindWord},
		{"table.xls", ole("Workbook"), FileKindExcel},
		{"slides.ppt", ole("PowerPoint Document"), FileKindPowerPoint},
		{"cv.docx", zipOf(t, map[string]string{"[Content_Types].xml": "x", "word/document.xml": "x"}), FileKindWord},
		{"table.xlsx", zipOf(t, map[string]string{"xl/workbook.xml": "x"}), FileKindExcel},
		{"slides.pptx", zipOf(t, map[string]string{"ppt/presentation.xml": "x"}), FileKindPowerPoint},
		{"cv.odt", zipOf(t, map[string]string{"mimetype": "application/vnd.oasis.opendocument.text"}), FileKindWord},
		{"table.ods", zipOf(t, map[string]string{"mimetype": "application/vnd.oasis.opendocument.spreadsheet"}), FileKindExcel},
		{"slides.odp", zipOf(t, map[string]string{"mimetype": "application/vnd.oasis.opendocument.presentation"}), FileKindPowerPoint},
		{"project.zip", zipOf(t, map[string]string{"main.go": "package main"}), FileKindArchive},
		{"project.7z", []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C, 0, 4}, FileKindArchive},
		{"project.rar", []byte("Rar!\x1a\x07\x01\x00"), FileKindArchive},
		{"project.tar", tarOf(), FileKindArchive},
		{"project.tgz", []byte{0x1F, 0x8B, 0x08, 0x00}, FileKindArchive},
		{"notes.txt", []byte("Привіт, світ\n"), FileKindText},
		{"README.md", []byte("\xEF\xBB\xBF# Title\n"), FileKindText},
		{"list.csv", []byte("name,score\nA,1\n"), FileKindText},
		{"letter.rtf", []byte(`{\rtf1\ansi Hello}`), FileKindText},
	}
	for _, tc := range cases {
		kind, contentType, err := DetectFileKind(tc.name, tc.data)
		if err != nil || kind != tc.kind || contentType == "" {
			t.Errorf("%s: kind=%q type=%q err=%v, want %q", tc.name, kind, contentType, err, tc.kind)
		}
	}
}

func TestDetectFileKindRefusesDangerousOrUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty.txt", nil},
		{"setup.exe", []byte("MZ\x90\x00")},
		{"tool.pdf", []byte("\x7fELF\x02\x01")},
		{"run.txt", []byte("#!/bin/sh\nrm -rf /\n")},
		{"page.txt", []byte("<!DOCTYPE html><html><script>alert(1)</script>")},
		{"logo.txt", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{"logo.svg", []byte(`<?xml version="1.0"?><svg/>`)},
		{"notes.bin", []byte("plain text with an unknown extension")},
		{"binary.txt", []byte("abc\x00def")},
		{"latin1.txt", []byte("caf\xe9")},
		{"sheet.xls", ole("SomethingElse")},
		{"broken.zip", []byte("PK\x03\x04garbage")},
	} {
		if kind, _, err := DetectFileKind(tc.name, tc.data); err == nil {
			t.Errorf("%s must be refused, got %q", tc.name, kind)
		}
	}
}

func TestLegacyFileKindsStillAllowed(t *testing.T) {
	block := eventContentModel.Block{FileTypes: []string{"doc", "zip"}}
	if !AllowsFile(block, FileKindWord) || !AllowsFile(block, FileKindArchive) || AllowsFile(block, FileKindExcel) {
		t.Fatal("doc and zip saved earlier mean Word and archives")
	}
	if !validFileKind("doc") || !validFileKind("powerpoint") || validFileKind("exe") {
		t.Fatal("valid kinds")
	}
}
