package eventFormModel

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// FileKind is a group of file formats an organizer may allow for a question.
type FileKind string

const (
	FileKindPDF        FileKind = "pdf"
	FileKindImage      FileKind = "image"
	FileKindWord       FileKind = "word"
	FileKindExcel      FileKind = "excel"
	FileKindPowerPoint FileKind = "powerpoint"
	FileKindText       FileKind = "text"
	FileKindArchive    FileKind = "archive"

	// Kinds saved before the formats were split; they mean Word and archives.
	legacyFileKindDoc FileKind = "doc"
	legacyFileKindZip FileKind = "zip"
)

func normalizeFileKind(kind string) FileKind {
	switch FileKind(kind) {
	case legacyFileKindDoc:
		return FileKindWord
	case legacyFileKindZip:
		return FileKindArchive
	default:
		return FileKind(kind)
	}
}

func validFileKind(kind string) bool {
	switch normalizeFileKind(kind) {
	case FileKindPDF, FileKindImage, FileKindWord, FileKindExcel, FileKindPowerPoint, FileKindText, FileKindArchive:
		return true
	default:
		return false
	}
}

// AllowsFile reports whether a file of this kind may answer the question.
func AllowsFile(block eventContentModel.Block, kind FileKind) bool {
	for _, allowed := range block.FileTypes {
		if normalizeFileKind(allowed) == kind {
			return true
		}
	}
	return false
}

var (
	errKind = errors.New("file type is not allowed")

	oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	// Executables are never accepted, whatever the question allows.
	executableMagic = [][]byte{
		[]byte("MZ"), []byte("\x7fELF"), []byte("#!"),
		{0xFE, 0xED, 0xFA, 0xCE}, {0xFE, 0xED, 0xFA, 0xCF}, {0xCE, 0xFA, 0xED, 0xFE}, {0xCF, 0xFA, 0xED, 0xFE}, {0xCA, 0xFE, 0xBA, 0xBE},
	}
	sevenZipMagic = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}
	rarMagic      = []byte("Rar!\x1a\x07")
	gzipMagic     = []byte{0x1F, 0x8B}
	// Markup a text file may not be: browsers would render it.
	markupSigns = []string{"<html", "<!doctype", "<script", "<svg", "<?xml", "<body", "<iframe"}
)

// Office documents inside an OLE container (legacy .doc/.xls/.ppt) are told
// apart by their main stream name in the directory (UTF-16LE).
var oleStreams = []struct {
	name        string
	kind        FileKind
	contentType string
}{
	{"WordDocument", FileKindWord, "application/msword"},
	{"Workbook", FileKindExcel, "application/vnd.ms-excel"},
	{"Book", FileKindExcel, "application/vnd.ms-excel"},
	{"PowerPoint Document", FileKindPowerPoint, "application/vnd.ms-powerpoint"},
}

// OOXML parts and ODF mimetypes of the Office kinds inside a ZIP container.
var (
	ooxmlParts = []struct {
		part        string
		kind        FileKind
		contentType string
	}{
		{"word/document.xml", FileKindWord, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"xl/workbook.xml", FileKindExcel, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"ppt/presentation.xml", FileKindPowerPoint, "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	}
	odfTypes = map[string]FileKind{
		"application/vnd.oasis.opendocument.text":         FileKindWord,
		"application/vnd.oasis.opendocument.spreadsheet":  FileKindExcel,
		"application/vnd.oasis.opendocument.presentation": FileKindPowerPoint,
	}
	textTypes = map[string]string{
		".txt": "text/plain; charset=utf-8",
		".md":  "text/markdown; charset=utf-8",
		".csv": "text/csv; charset=utf-8",
	}
)

func utf16le(name string) []byte {
	out := make([]byte, 0, len(name)*2)
	for _, r := range name {
		out = append(out, byte(r), 0)
	}
	return out
}

// DetectFileKind identifies a file by its content, never by its name alone:
//   - PDF and PNG/JPEG/GIF/WebP images by their signatures;
//   - legacy Office files by the OLE header and their main stream;
//   - OOXML and ODF documents by the parts inside their ZIP container;
//   - ZIP, 7z, RAR, tar and gzip (.tar.gz) archives by their magic bytes;
//   - RTF by its header, and .txt/.md/.csv only when the content is UTF-8
//     text without markup.
//
// Executables, SVG and HTML are always refused. It returns the kind and the
// content type to serve the file with.
func DetectFileKind(name string, data []byte) (FileKind, string, error) {
	if len(data) == 0 {
		return "", "", errKind
	}
	for _, magic := range executableMagic {
		if bytes.HasPrefix(data, magic) {
			return "", "", errKind
		}
	}
	switch sniffed := http.DetectContentType(data); sniffed {
	case "application/pdf":
		return FileKindPDF, sniffed, nil
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return FileKindImage, sniffed, nil
	}
	switch {
	case bytes.HasPrefix(data, oleMagic):
		for _, stream := range oleStreams {
			if bytes.Contains(data, utf16le(stream.name)) {
				return stream.kind, stream.contentType, nil
			}
		}
		return "", "", errKind
	case bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06")):
		return zipKind(data)
	case bytes.HasPrefix(data, sevenZipMagic):
		return FileKindArchive, "application/x-7z-compressed", nil
	case bytes.HasPrefix(data, rarMagic):
		return FileKindArchive, "application/vnd.rar", nil
	case bytes.HasPrefix(data, gzipMagic):
		return FileKindArchive, "application/gzip", nil
	case len(data) > 262 && string(data[257:262]) == "ustar":
		return FileKindArchive, "application/x-tar", nil
	case bytes.HasPrefix(data, []byte(`{\rtf`)):
		return FileKindText, "application/rtf", nil
	}
	contentType, ok := textTypes[strings.ToLower(path.Ext(name))]
	if !ok || !plainText(data) {
		return "", "", errKind
	}
	return FileKindText, contentType, nil
}

func zipKind(data []byte) (FileKind, string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", errKind
	}
	names := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		names[entry.Name] = entry
	}
	for _, part := range ooxmlParts {
		if _, ok := names[part.part]; ok {
			return part.kind, part.contentType, nil
		}
	}
	if entry, ok := names["mimetype"]; ok && entry.UncompressedSize64 < 128 {
		if rc, openErr := entry.Open(); openErr == nil {
			raw, _ := io.ReadAll(io.LimitReader(rc, 128))
			_ = rc.Close()
			if kind, known := odfTypes[strings.TrimSpace(string(raw))]; known {
				return kind, strings.TrimSpace(string(raw)), nil
			}
		}
	}
	return FileKindArchive, "application/zip", nil
}

// plainText accepts UTF-8 text (an optional BOM) without NUL bytes or markup.
func plainText(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	head := strings.ToLower(string(data[:min(len(data), 4096)]))
	for _, sign := range markupSigns {
		if strings.Contains(head, sign) {
			return false
		}
	}
	return true
}
