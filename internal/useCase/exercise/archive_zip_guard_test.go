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
	requireTooLarge(t, err, "guard")
}

func TestOpenZipAtRefusesHugeDirectorySize(t *testing.T) {
	b := guardTestZip(t, 3)
	binary.LittleEndian.PutUint32(b[eocdOf(t, b)+12:], 0x7FFFFFFF)
	_, _, err := openZipAt(bytes.NewReader(b), int64(len(b)), "")
	requireTooLarge(t, err, "guard")
}

func TestOpenZipAtRefusesHugeZip64Record(t *testing.T) {
	b := guardTestZip(t, 3)
	e := eocdOf(t, b)
	rec := make([]byte, zip64EOCDMinLen)
	binary.LittleEndian.PutUint32(rec, zip64EOCDSig)
	binary.LittleEndian.PutUint64(rec[4:], zip64EOCDMinLen-12)
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
	binary.LittleEndian.PutUint64(out[e-zip64LocatorLen+8:], uint64(e-zip64LocatorLen-zip64EOCDMinLen))
	_, _, err := openZipAt(bytes.NewReader(out), int64(len(out)), "")
	requireTooLarge(t, err, "guard")
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
