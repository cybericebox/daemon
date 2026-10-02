// Command errorcatalog emits the API error catalog as JSON: { "<FullCode>": "<English message>" }.
//
// The backend is the single source of truth for both the numeric FullCode a
// response carries (Status.Code) and the English message. Frontends localize by
// code against this catalog, so they must never hand-copy codes or English text —
// they regenerate this file and translate the values.
//
// FullCode = informCode*10000 + objectCode*100 + detailCode (see pkg/err/code.go).
// Both code tables are plain iota blocks (pkg/err/code.go, internal/model/code.go);
// mirrored here as maps. If those iota orders ever change, update these maps — the
// self-check below fails loudly on an unknown base or object constant.
//
// Usage: go run ./tools/errorcatalog > errors.en.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// informByBase maps the base error var (pkg/err) to its informCode.
// InformCodeInternal(0) is intentionally absent: internal (500) errors are shown
// as a generic message, never localized per-code.
var informByBase = map[string]int{
	"ErrInvalidData":     2,
	"ErrObjectNotFound":  3,
	"ErrObjectExists":    4,
	"ErrUnauthenticated": 5,
	"ErrForbidden":       6,
	"ErrConflict":        7,
}

// objectByName maps the model.<X>ObjectCode constant to its iota value.
var objectByName = map[string]int{
	"PlatformObjectCode":          0,
	"SettingObjectCode":           1,
	"NotificationObjectCode":      2,
	"UserObjectCode":              3,
	"AuthObjectCode":              4,
	"TemporalCodeObjectCode":      5,
	"AuthRecaptchaObjectCode":     6,
	"IPAMObjectCode":              7,
	"WgKeyGenObjectCode":          8,
	"ExerciseObjectCode":          9,
	"MediaObjectCode":             10,
	"EventObjectCode":             11,
	"EventConfigObjectCode":       12,
	"ParticipantObjectCode":       13,
	"InfrastructureObjectCode":    14,
	"VPNConfigObjectCode":         15,
	"EventManagerObjectCode":      16,
	"EventTeamObjectCode":         17,
	"EventExerciseObjectCode":     18,
	"EventChallengeObjectCode":    19,
	"EventStandObjectCode":        20,
	"MailObjectCode":              21,
	"EventAnalyticsObjectCode":    22,
	"PlatformAnalyticsObjectCode": 23,
	"ErrorJournalObjectCode":      24,
	"ResourceCalendarObjectCode":  25,
}

// declRe captures one builder chain: var name, base error, object constant, and
// the terminating detail code. (?sU) = dotall + ungreedy so it spans line breaks
// and stops at the first WithDetailCode.
var declRe = regexp.MustCompile(`(?sU)(\w+)\s*=\s*err\.(\w+)\.\s*WithObjectCode\(model\.(\w+)\).*WithDetailCode\((\d+)\)`)

// msgRe pulls the English message out of a matched declaration block.
var msgRe = regexp.MustCompile(`WithMessage\(\s*"((?:[^"\\]|\\.)*)"`)

func main() {
	out, err := generate("internal/model", "pkg/ipam")
	if err != nil {
		fmt.Fprintln(os.Stderr, "errorcatalog:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// generate scans every non-test .go file under the roots and renders the catalog.
func generate(roots ...string) (string, error) {
	catalog := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range declRe.FindAllSubmatch(data, -1) {
				base := string(m[2])
				objName := string(m[3])
				detail, _ := strconv.Atoi(string(m[4]))

				inform, ok := informByBase[base]
				if !ok {
					// Internal (or unknown) base — skipped for internal, but a
					// genuinely unknown base is a bug in the maps above.
					if base == "ErrInternal" {
						continue
					}
					return fmt.Errorf("%s: unknown base error %q (add it to informByBase)", path, base)
				}
				obj, ok := objectByName[objName]
				if !ok {
					return fmt.Errorf("%s: unknown object constant %q (add it to objectByName)", path, objName)
				}

				full := inform*10000 + obj*100 + detail
				msg := ""
				if mm := msgRe.FindSubmatch(m[0]); mm != nil {
					msg = string(mm[1])
				}
				catalog[strconv.Itoa(full)] = msg
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}

	// Deterministic output: sort keys numerically.
	keys := make([]int, 0, len(catalog))
	for k := range catalog {
		n, _ := strconv.Atoi(k)
		keys = append(keys, n)
	}
	sort.Ints(keys)

	out := make([]string, 0, len(keys))
	out = append(out, "{")
	for i, k := range keys {
		comma := ","
		if i == len(keys)-1 {
			comma = ""
		}
		val, _ := json.Marshal(catalog[strconv.Itoa(k)])
		out = append(out, fmt.Sprintf("  %q: %s%s", strconv.Itoa(k), val, comma))
	}
	out = append(out, "}")
	return strings.Join(out, "\n") + "\n", nil
}
