package eventAnalyticsModel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/gofrs/uuid"
	"github.com/klauspost/compress/zstd"
)

// LabArchiveCodecZstdNDJSON names the archive chunk format: zstd-compressed
// NDJSON, one event_lab_observations row per line with every column.
const LabArchiveCodecZstdNDJSON = "zstd-ndjson-v1"

// LabArchiveChunkRows bounds the rows of one archive chunk, so a chunk is
// read and compressed in memory without trouble even for a busy hour.
const LabArchiveChunkRows = 4000

// LabArchiveClosedAfter is how long after an hour ends its observations are
// archived, so a late-committed row still lands in its chunk.
const LabArchiveClosedAfter = 5 * time.Minute

// LabArchiveClosedBefore is the instant before which received rows belong to
// closed hours at now: only they are archived.
func LabArchiveClosedBefore(now time.Time) time.Time {
	return now.Add(-LabArchiveClosedAfter).UTC().Truncate(time.Hour)
}

// LabObservation is one raw observation row as the archive holds it. Payload
// is the lab group snapshot or delta as sent by the agent.
type LabObservation struct {
	ID            uuid.UUID       `json:"id"`
	EventID       uuid.UUID       `json:"event_id"`
	EventTeamID   uuid.UUID       `json:"event_team_id"`
	LabGroupName  string          `json:"lab_group_name"`
	AgentID       string          `json:"agent_id"`
	Sequence      int64           `json:"sequence"`
	ObservedAt    time.Time       `json:"observed_at"`
	ReceivedAt    time.Time       `json:"received_at"`
	SchemaVersion int32           `json:"schema_version"`
	Snapshot      bool            `json:"snapshot"`
	Payload       json.RawMessage `json:"payload"`
}

// CompressLabArchive packs observation JSON lines into one chunk.
func CompressLabArchive(lines []string) ([]byte, error) {
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if _, err = io.WriteString(enc, line); err != nil {
			_ = enc.Close()
			return nil, err
		}
		if _, err = enc.Write([]byte{'\n'}); err != nil {
			_ = enc.Close()
			return nil, err
		}
	}
	if err = enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParseLabObservationLine reads one observation JSON line.
func ParseLabObservationLine(line string) (LabObservation, error) {
	var o LabObservation
	if err := json.Unmarshal([]byte(line), &o); err != nil {
		return LabObservation{}, fmt.Errorf("lab archive: decode line: %w", err)
	}
	return o, nil
}

// LabArchiveLines calls fn for every JSON line of a chunk, in the order the
// chunk holds them (team, lab group, observation time). The slice is valid
// only during the call.
func LabArchiveLines(codec string, data []byte, fn func(line []byte) error) error {
	if codec != LabArchiveCodecZstdNDJSON {
		return fmt.Errorf("lab archive: unknown codec %q", codec)
	}
	dec, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer dec.Close()
	reader := bufio.NewReaderSize(dec, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if ferr := fn(trimmed); ferr != nil {
				return ferr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// DecodeLabArchive calls fn for every observation of a chunk, in the order the
// chunk holds them. Rollups that depend on arrival order sort what they read
// by ReceivedAt and ID.
func DecodeLabArchive(codec string, data []byte, fn func(LabObservation) error) error {
	return LabArchiveLines(codec, data, func(line []byte) error {
		var o LabObservation
		if err := json.Unmarshal(line, &o); err != nil {
			return fmt.Errorf("lab archive: decode line: %w", err)
		}
		return fn(o)
	})
}

// LabIPWindow is a period in which an IP belonged to an erased client in one lab group: from the first to the last
// observation that gave it to the client.
type LabIPWindow struct {
	TeamID uuid.UUID `json:"team_id"`
	Group  string    `json:"group"`
	IP     string    `json:"ip"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// MergeLabIPWindows joins two lists: windows of the same team, group and IP become one spanning both.
func MergeLabIPWindows(a, b []LabIPWindow) []LabIPWindow {
	type key struct {
		team  uuid.UUID
		group string
		ip    string
	}
	merged := map[key]LabIPWindow{}
	var order []key
	for _, w := range append(append([]LabIPWindow{}, a...), b...) {
		k := key{w.TeamID, w.Group, w.IP}
		cur, ok := merged[k]
		if !ok {
			merged[k] = w
			order = append(order, k)
			continue
		}
		if w.From.Before(cur.From) {
			cur.From = w.From
		}
		if w.To.After(cur.To) {
			cur.To = w.To
		}
		merged[k] = cur
	}
	out := make([]LabIPWindow, 0, len(order))
	for _, k := range order {
		out = append(out, merged[k])
	}
	return out
}

// LabArchiveErasure is what to remove from an archive for one erased participant: the client name becomes
// Pseudonym and, inside the Windows, every JSON string equal to the window's IP (or the same with a /32 mask) is
// emptied in the rows of that lab group, except in a row that gives the IP to another client.
type LabArchiveErasure struct {
	Pseudonym string
	Windows   []LabIPWindow
}

// ScrubLabArchive rewrites an archive chunk without the link to erased people: every VPN client name in erasures
// (real name -> erasure) is replaced by its pseudonym wherever it appears in the rows, and the IPs of the windows are
// emptied (see LabIPWindow). Nothing else changes. It reports whether anything changed; a chunk that does not
// mention the names or IPs comes back untouched, so a repeated scrub is a no-op. A row that holds a window's IP but
// cannot be read is an error: the erasure would be incomplete.
func ScrubLabArchive(codec string, data []byte, erasures map[string]LabArchiveErasure) ([]byte, bool, error) {
	var lines []string
	changed := false
	err := LabArchiveLines(codec, data, func(line []byte) error {
		out := line
		for name, erasure := range erasures {
			if bytes.Contains(out, []byte(name)) {
				out = bytes.ReplaceAll(out, []byte(name), []byte(erasure.Pseudonym))
			}
			var err error
			if out, err = blankLabLineIPs(out, erasure.Windows, []string{name, erasure.Pseudonym}); err != nil {
				return err
			}
		}
		if !bytes.Equal(out, line) {
			changed = true
		}
		lines = append(lines, string(out))
		return nil
	})
	if err != nil || !changed {
		return data, false, err
	}
	packed, err := CompressLabArchive(lines)
	if err != nil {
		return nil, false, err
	}
	return packed, true, nil
}

// labRow is what the archive keeps of a row that the IP windows need.
type labRow struct {
	TeamID       uuid.UUID `json:"event_team_id"`
	LabGroupName string    `json:"lab_group_name"`
	ObservedAt   time.Time `json:"observed_at"`
	Payload      struct {
		Clients []json.RawMessage `json:"clients"`
	} `json:"payload"`
}

// labRowClients reads a row's clients tolerantly: a client entry that cannot be read is skipped unless it mentions
// one of the names (then the row cannot be trusted and the error says so).
func labRowClients(row labRow, names []string) (map[string]string, error) {
	assigned := map[string]string{}
	for _, raw := range row.Payload.Clients {
		var c struct {
			Name   string `json:"name"`
			Status struct {
				AssignedIP string `json:"assignedIp"`
			} `json:"status"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			for _, n := range names {
				if bytes.Contains(raw, []byte(n)) {
					return nil, fmt.Errorf("lab archive: a client entry that names an erased client cannot be read: %w", err)
				}
			}
			continue
		}
		if c.Status.AssignedIP != "" {
			assigned[c.Status.AssignedIP] = c.Name
		}
	}
	return assigned, nil
}

// blankLabLineIPs empties the IPs of the windows in one archive row (see LabIPWindow). A row that holds none of the
// windows' IPs is returned as it is without being parsed.
func blankLabLineIPs(line []byte, windows []LabIPWindow, names []string) ([]byte, error) {
	mentions := false
	for _, w := range windows {
		if bytes.Contains(line, []byte(`"`+w.IP)) {
			mentions = true
			break
		}
	}
	if !mentions {
		return line, nil
	}
	var row labRow
	if err := json.Unmarshal(line, &row); err != nil {
		return nil, fmt.Errorf("lab archive: a row that holds an IP of an erased client cannot be read: %w", err)
	}
	owners, err := labRowClients(row, names)
	if err != nil {
		return nil, err
	}
	out := line
	for _, w := range windows {
		if w.TeamID != row.TeamID || w.Group != row.LabGroupName || row.ObservedAt.Before(w.From) || row.ObservedAt.After(w.To) {
			continue
		}
		if owner, taken := owners[w.IP]; taken && !containsString(names, owner) {
			continue
		}
		out = bytes.ReplaceAll(out, []byte(`"`+w.IP+`"`), []byte(`""`))
		out = bytes.ReplaceAll(out, []byte(`"`+w.IP+`/32"`), []byte(`""`))
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// LabArchiveClientIPWindows lists when, in which lab group, a chunk gave which IP to the named VPN clients, so
// erasure on request can remove the IPs in the rows around them. It fails closed: a row that mentions one of the
// names but cannot be read is an error (the erasure would be incomplete), while a row that mentions none is not
// looked at. Rows are read tolerantly: one odd client does not hide the others.
func LabArchiveClientIPWindows(codec string, data []byte, names []string) ([]LabIPWindow, error) {
	var found []LabIPWindow
	err := LabArchiveLines(codec, data, func(line []byte) error {
		mentioned := false
		for _, n := range names {
			if bytes.Contains(line, []byte(n)) {
				mentioned = true
				break
			}
		}
		if !mentioned {
			return nil
		}
		var row labRow
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("lab archive: a row that names an erased client cannot be read: %w", err)
		}
		for _, raw := range row.Payload.Clients {
			var c struct {
				Name   string `json:"name"`
				Status struct {
					AssignedIP string `json:"assignedIp"`
				} `json:"status"`
			}
			if err := json.Unmarshal(raw, &c); err != nil {
				if containsAny(raw, names) {
					return fmt.Errorf("lab archive: a client entry that names an erased client cannot be read: %w", err)
				}
				continue
			}
			if c.Status.AssignedIP != "" && containsString(names, c.Name) {
				found = append(found, LabIPWindow{
					TeamID: row.TeamID, Group: row.LabGroupName, IP: c.Status.AssignedIP, From: row.ObservedAt, To: row.ObservedAt,
				})
			}
		}
		return nil
	})
	return MergeLabIPWindows(nil, found), err
}

func containsAny(raw []byte, names []string) bool {
	for _, n := range names {
		if bytes.Contains(raw, []byte(n)) {
			return true
		}
	}
	return false
}
