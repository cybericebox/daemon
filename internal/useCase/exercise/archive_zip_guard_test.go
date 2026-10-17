package exercise

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	yzip "github.com/yeka/zip"
)

const (
	zipEOCDLen      = 22
	zip64LocatorLen = 20
	zip64EOCDLen    = 56
	zipEOCDSig      = 0x06054b50
	zip64LocatorSig = 0x07064b50
	zip64EOCDSig    = 0x06064b50
)

func guardTestZip(t *testing.T, n int) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for i := 0; i < n; i++ {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("f%d.txt", i), Method: zip.Store})
		require.NoError(t, err)
		_, _ = w.Write([]byte("x"))
	}
	require.NoError(t, zw.Close())
	return b.Bytes()
}

func eocdOf(t *testing.T, b []byte) int {
	t.Helper()
	i := bytes.LastIndex(b, []byte{0x50, 0x4b, 0x05, 0x06})
	require.GreaterOrEqual(t, i, 0)
	return i
}

func TestOpenZipAtRefusesHugeEntryCountInEndRecord(t *testing.T) {
	b := guardTestZip(t, 3)
	binary.LittleEndian.PutUint16(b[eocdOf(t, b)+10:], 60000)
	_, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	require.Error(t, err, "a lying count is refused")
}

func TestOpenZipAtRefusesHugeDirectorySize(t *testing.T) {
	b := guardTestZip(t, 3)
	binary.LittleEndian.PutUint32(b[eocdOf(t, b)+12:], 0x7FFFFFFF)
	_, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	require.Error(t, err, "a lying size is refused")
}

func TestOpenZipAtRefusesHugeZip64Record(t *testing.T) {
	b := guardTestZip(t, 3)
	e := eocdOf(t, b)
	rec := make([]byte, zip64EOCDLen)
	binary.LittleEndian.PutUint32(rec, zip64EOCDSig)
	binary.LittleEndian.PutUint64(rec[4:], zip64EOCDLen-12)
	binary.LittleEndian.PutUint64(rec[32:], 1<<40)
	binary.LittleEndian.PutUint64(rec[40:], 1<<40)
	loc := make([]byte, zip64LocatorLen)
	binary.LittleEndian.PutUint32(loc, zip64LocatorSig)
	binary.LittleEndian.PutUint64(loc[8:], uint64(e))
	binary.LittleEndian.PutUint32(loc[16:], 1)
	out := append(append(append(append([]byte{}, b[:e]...), rec...), loc...), b[e:]...)
	e += len(rec) + len(loc)
	binary.LittleEndian.PutUint16(out[e+10:], 0xFFFF)
	// the zip64 record sits where the locator says
	binary.LittleEndian.PutUint64(out[e-zip64LocatorLen+8:], uint64(e-zip64LocatorLen-zip64EOCDLen))
	_, _, err := openZipAt(bytes.NewReader(out), int64(len(out)), "")
	require.Error(t, err, "a lying zip64 record is refused")
}

func TestOpenZipAtLegitArchivesStillOpen(t *testing.T) {
	b := guardTestZip(t, 5)
	entries, protected, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	require.NoError(t, err)
	require.False(t, protected)
	require.Len(t, entries, 5)

	var enc bytes.Buffer
	yw := yzip.NewWriter(&enc)
	w, err := yw.Encrypt("a.txt", "pw", yzip.AES256Encryption)
	require.NoError(t, err)
	_, _ = w.Write([]byte("secret"))
	require.NoError(t, yw.Close())
	entries, protected, err = openZipAt(bytes.NewReader(enc.Bytes()), int64(enc.Len()), "pw")
	require.NoError(t, err)
	require.True(t, protected)
	require.Len(t, entries, 1)
}

func TestOpenZipAtZip64EndRecordStillOpens(t *testing.T) {
	old := limits
	limits.Entries = 70000
	t.Cleanup(func() { limits = old })
	b := guardTestZip(t, 0xFFFF+5) // the writer then writes the zip64 records
	require.Contains(t, string(b), "\x50\x4b\x06\x06")
	entries, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	require.NoError(t, err)
	require.Len(t, entries, 0xFFFF+5)
}

func TestOpenZipAtZip64OverTheCapRefused(t *testing.T) {
	b := guardTestZip(t, 0xFFFF+5)
	_, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	requireTooLarge(t, err, "guard")
}

// A fake end record inside the real one's comment (small count, a comment length that does not fit) must not hide
// the real record: the reader skips the fake one, so the guard does too.
func TestOpenZipAtFakeEndRecordInCommentDoesNotHideHugeCount(t *testing.T) {
	b := guardTestZip(t, 3)
	e := eocdOf(t, b)
	fake := make([]byte, zipEOCDLen)
	binary.LittleEndian.PutUint32(fake, zipEOCDSig)
	binary.LittleEndian.PutUint16(fake[10:], 1)
	binary.LittleEndian.PutUint16(fake[20:], 500) // comment longer than what is left
	out := append(append([]byte{}, b...), fake...)
	binary.LittleEndian.PutUint16(out[e+10:], 60000)
	binary.LittleEndian.PutUint16(out[e+20:], uint16(len(fake)))
	_, _, err := openZipAt(bytes.NewReader(out), int64(len(out)), "")
	require.Error(t, err, "the real record is the one the reader picks")
}

func longNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%03d-%s", i, string(bytes.Repeat([]byte("a"), 3000)))
	}
	return names
}

// The directory is really that large (not a lie): the bytes read are capped, so the reader never holds it.
func TestOpenZipAtRealOversizeDirectoryPlain(t *testing.T) {
	old := limits
	limits.Entries = 2
	t.Cleanup(func() { limits = old })
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, name := range longNames(60) {
		_, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	_, _, err := openZipAt(bytes.NewReader(b.Bytes()), int64(b.Len()), "")
	requireTooLarge(t, err, "plain directory")
}

func TestOpenZipAtRealOversizeDirectoryProtected(t *testing.T) {
	old := limits
	limits.Entries = 2
	t.Cleanup(func() { limits = old })
	var b bytes.Buffer
	yw := yzip.NewWriter(&b)
	for _, name := range longNames(60) {
		w, err := yw.Encrypt(name, "pw", yzip.AES256Encryption)
		require.NoError(t, err)
		_, _ = w.Write([]byte("x"))
	}
	require.NoError(t, yw.Close())
	_, _, err := openZipAt(bytes.NewReader(b.Bytes()), int64(b.Len()), "pw")
	requireTooLarge(t, err, "protected directory")
}

// More headers than the entry cap within the byte budget is refused by the count.
func TestOpenZipAtOverEntryCountRefused(t *testing.T) {
	old := limits
	limits.Entries = 5
	t.Cleanup(func() { limits = old })
	b := guardTestZip(t, 20)
	_, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	requireTooLarge(t, err, "entry count")
}

// No end record at all: refused, never passed through.
func TestOpenZipAtNoEndRecordRefused(t *testing.T) {
	b := guardTestZip(t, 3)
	b = b[:eocdOf(t, b)]
	_, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	require.Error(t, err)
	_, _, err = openZipAt(bytes.NewReader([]byte("not a zip")), 9, "")
	require.Error(t, err)
}
