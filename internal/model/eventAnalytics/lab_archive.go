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

// LabArchiveErasedClient replaces the name of a deleted participant's VPN
// client in a scrubbed archive.
const LabArchiveErasedClient = "p-deleted"

// ScrubLabArchive rewrites an archive chunk without the given VPN clients: their
// names become LabArchiveErasedClient everywhere in the payloads and the IPs
// the platform assigned to them are blanked. It reports whether anything
// changed; a chunk that does not mention them comes back untouched, so a
// repeated scrub is a no-op.
func ScrubLabArchive(codec string, data []byte, clients []string) ([]byte, bool, error) {
	var lines []string
	changed := false
	err := LabArchiveLines(codec, data, func(line []byte) error {
		mentioned := false
		for _, c := range clients {
			if bytes.Contains(line, []byte(c)) {
				mentioned = true
				break
			}
		}
		if !mentioned {
			lines = append(lines, string(line))
			return nil
		}
		scrubbed, err := scrubLabArchiveLine(line, clients)
		if err != nil {
			return err
		}
		changed = true
		lines = append(lines, string(scrubbed))
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

func scrubLabArchiveLine(line []byte, clients []string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var row map[string]any
	if err := dec.Decode(&row); err != nil {
		return nil, fmt.Errorf("lab archive: decode line: %w", err)
	}
	erased := map[string]bool{}
	for _, c := range clients {
		erased[c] = true
	}
	var ips []string
	if payload, ok := row["payload"].(map[string]any); ok {
		list, _ := payload["clients"].([]any)
		for _, item := range list {
			client, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name, _ := client["name"].(string); !erased[name] {
				continue
			}
			if status, ok := client["status"].(map[string]any); ok {
				if ip, _ := status["assignedIp"].(string); ip != "" {
					ips = append(ips, ip)
					status["assignedIp"] = ""
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(row); err != nil {
		return nil, err
	}
	out := bytes.TrimSpace(buf.Bytes())
	for _, ip := range ips {
		out = bytes.ReplaceAll(out, []byte(`"`+ip+`"`), []byte(`""`))
	}
	for _, c := range clients {
		out = bytes.ReplaceAll(out, []byte(c), []byte(LabArchiveErasedClient))
	}
	return out, nil
}
