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

// DecodeLabArchive calls fn for every observation of a chunk, in the order the
// chunk holds them (team, lab group, observation time). Rollups that depend on
// arrival order sort what they read by ReceivedAt and ID.
func DecodeLabArchive(codec string, data []byte, fn func(LabObservation) error) error {
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
		if len(bytes.TrimSpace(line)) > 0 {
			var o LabObservation
			if uerr := json.Unmarshal(line, &o); uerr != nil {
				return fmt.Errorf("lab archive: decode line: %w", uerr)
			}
			if ferr := fn(o); ferr != nil {
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
