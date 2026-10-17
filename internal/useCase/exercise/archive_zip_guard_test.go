package exercise

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	yzip "github.com/yeka/zip"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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

// tailReaderAt is a huge archive that is zeros but for its tail.
type tailReaderAt struct {
	size int64
	tail []byte
}

func (r tailReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.size {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.size-off {
		n = int(r.size - off)
	}
	clear(p[:n])
	if start := r.size - int64(len(r.tail)); off+int64(n) > start {
		from := max(off, start)
		copy(p[from-off:n], r.tail[from-start:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// hugeLyingTail is the tail of a 10 GiB archive: a zip64 record declaring `declared` entries (and a plain end
// record with the matching sentinels), or only a plain end record declaring `plain` entries.
func hugeLyingTail(declared uint64, plain uint16, zip64 bool) tailReaderAt {
	size := int64(10 << 30)
	var tail []byte
	rec := make([]byte, zip64EOCDLen)
	loc := make([]byte, zip64LocatorLen)
	eocd := make([]byte, zipEOCDLen)
	binary.LittleEndian.PutUint32(eocd, zipEOCDSig)
	binary.LittleEndian.PutUint16(eocd[8:], plain)
	binary.LittleEndian.PutUint16(eocd[10:], plain)
	if zip64 {
		binary.LittleEndian.PutUint32(rec, zip64EOCDSig)
		binary.LittleEndian.PutUint64(rec[4:], zip64EOCDLen-12)
		binary.LittleEndian.PutUint64(rec[24:], declared)
		binary.LittleEndian.PutUint64(rec[32:], declared)
		binary.LittleEndian.PutUint64(rec[40:], 1<<20)
		binary.LittleEndian.PutUint64(rec[48:], 1<<20)
		binary.LittleEndian.PutUint32(loc, zip64LocatorSig)
		binary.LittleEndian.PutUint64(loc[8:], uint64(size)-zipEOCDLen-zip64LocatorLen-zip64EOCDLen)
		binary.LittleEndian.PutUint32(loc[16:], 1)
		binary.LittleEndian.PutUint16(eocd[8:], 0xFFFF)
		binary.LittleEndian.PutUint16(eocd[10:], 0xFFFF)
		binary.LittleEndian.PutUint32(eocd[12:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(eocd[16:], 0xFFFFFFFF)
		tail = append(append(append(tail, rec...), loc...), eocd...)
	} else {
		binary.LittleEndian.PutUint32(eocd[12:], 1<<20)
		tail = eocd
	}
	return tailReaderAt{size: size, tail: tail}
}

func allocatedBy(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestOpenZipAtDeclaredHugeCountRefusedBeforeAllocation(t *testing.T) {
	for name, r := range map[string]tailReaderAt{
		"zip64 200M": hugeLyingTail(200_000_000, 0, true),
		"plain 5000": hugeLyingTail(0, 5000, false),
	} {
		t.Run(name+" plain reader", func(t *testing.T) {
			var err error
			got := allocatedBy(func() { _, _, err = openZipAt(r, r.size, "") })
			requireTooLarge(t, err, name)
			require.Less(t, got, uint64(8<<20))
		})
		t.Run(name+" protected reader", func(t *testing.T) {
			var err error
			budget := newOpenBudgetReaderAt(r)
			got := allocatedBy(func() { _, err = yzip.NewReader(budget, r.size) })
			require.Error(t, err)
			require.ErrorIs(t, budget.finish(), exerciseModel.ErrArchiveTooLarge.Err())
			require.Less(t, got, uint64(8<<20))
		})
	}
}

// zip64Wrapped is b with a zip64 end record declaring `count` entries and the plain record saturated, as Info-ZIP
// and 7z write any zip64 archive.
func zip64Wrapped(t *testing.T, b []byte, count uint64) []byte {
	t.Helper()
	e := eocdOf(t, b)
	dirSize := binary.LittleEndian.Uint32(b[e+12:])
	dirOff := binary.LittleEndian.Uint32(b[e+16:])
	rec := make([]byte, zip64EOCDLen)
	binary.LittleEndian.PutUint32(rec, zip64EOCDSig)
	binary.LittleEndian.PutUint64(rec[4:], zip64EOCDLen-12)
	binary.LittleEndian.PutUint64(rec[24:], count)
	binary.LittleEndian.PutUint64(rec[32:], count)
	binary.LittleEndian.PutUint64(rec[40:], uint64(dirSize))
	binary.LittleEndian.PutUint64(rec[48:], uint64(dirOff))
	loc := make([]byte, zip64LocatorLen)
	binary.LittleEndian.PutUint32(loc, zip64LocatorSig)
	binary.LittleEndian.PutUint64(loc[8:], uint64(e))
	binary.LittleEndian.PutUint32(loc[16:], 1)
	out := append(append(append(append([]byte{}, b[:e]...), rec...), loc...), b[e:]...)
	e += len(rec) + len(loc)
	binary.LittleEndian.PutUint16(out[e+8:], 0xFFFF)
	binary.LittleEndian.PutUint16(out[e+10:], 0xFFFF)
	binary.LittleEndian.PutUint32(out[e+12:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(out[e+16:], 0xFFFFFFFF)
	return out
}

func TestOpenZipAtSaturatedPlainRecordWithSmallZip64CountOpens(t *testing.T) {
	out := zip64Wrapped(t, guardTestZip(t, 3), 3)
	entries, _, err := openZipAt(bytes.NewReader(out), int64(len(out)), "")
	require.NoError(t, err)
	require.Len(t, entries, 3)
}

func TestOpenZipAtSaturatedPlainRecordWithHugeZip64CountRefused(t *testing.T) {
	out := zip64Wrapped(t, guardTestZip(t, 3), 200_000_000)
	_, _, err := openZipAt(bytes.NewReader(out), int64(len(out)), "")
	requireTooLarge(t, err, "zip64 count")
}
