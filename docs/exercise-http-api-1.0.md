# Exercise HTTP API: changes of the document shape cib-exercise/1.0

For the catalog frontend, which is **not** updated yet. The API has one shape: the old fields are gone (no
compatibility). Contract of the document: `docs/specs/2026-11-11-exercise-schema-1.0.md` of the platform repo; the
archive format is described, with every limit, in `internal/useCase/exercise/example/exercise.yaml`.

JSON names below are the ones of the HTTP DTOs (`internal/delivery/controller/http/handler/exercise/dto.go`, PascalCase).
The generated OpenAPI (`apidocs/swagger.*`) carries the same.

## Removed

| Where | Field | Where it went |
|---|---|---|
| Task | `Flag` | `Check.Answer.Values` (with `Check.Answer.Type = "flag"`) |
| Task | `LinkedDeviceID` | gone; a value reaches a device only through that device's `Bootstrap` |
| Task | `DeviceFlagVar` | an entry of `Bootstrap.Env` of the device: `{Name: <var>, Value: "{{task.<Key>}}"}` |
| Device | `EnvVars` | `Bootstrap.Env` (same element: `Name`, `Value`, `Secret`, response-only `HasValue`, `ValueGroup`) |

Error codes retired: 20924 (flag references a device), 20942 (flag target variable conflict).

## Added

### Task

| Field | Type | Meaning and limits |
|---|---|---|
| `Key` | string | Name of the task in its variant: `^[a-z][a-z0-9_]{0,31}$`, unique in the variant. Used by `{{task.<Key>}}` and `Requires`. Empty on save: the backend sets a stable `task_<position>` (the key of the first variant's task at the same position when the position has one). Send the key back unchanged. |
| `Requires` | string[] | Keys of tasks of the same variant that must be done first. At most 32; no repeats, no self reference, no cycles, every key must exist. The gating at solve time is a follow-up; this is the data. |
| `Check` | object | `{Answer}` or `{State}`: exactly one (a draft may have neither; publishing needs one; the kind and the answer type are the same in every variant). |

`Check.Answer` (the participant types a value):

| Field | Type | Limits |
|---|---|---|
| `Type` | `flag` \| `text` | required |
| `Values` | string[] | flag: each `ICE{...}`, or `template:ICE{...}`; none = a random flag per instance. text: required, 1-100 values, each 1-256 characters, no control characters, `template:<pattern>` allowed (a pattern with at least one random position, classes `[a-z0-9]`, `\d \l \u`, repeat `{n}` 1-64). Without delivery into a lab (the task's `{{task.<Key>}}` referenced by a bootstrap) a task has exactly one fixed value at publish. |
| `InputHint` | string | Shown inside the answer field; at most 100 characters, no control characters. Both types. Reaches participants in the pinned task snapshot as `Answer.input_hint`. |
| `IgnoreCase` | bool | text only (an error on flag) |
| `Trim` | bool \| null | text only; absent = on. An error on flag. |

`Check.State` (the lab checks devices; Academy only, forbidden on events later):

| Field | Type | Limits |
|---|---|---|
| `Mode` | `once` \| `continuous` | default `once` |
| `Interval` | string | Go duration, 5s to 24h; `""` or `"0"` = no schedule |
| `Manual` | bool | the participant may press "check now" |
| `Conditions` | Condition[] | 1 to 20 |

`Condition`: `DeviceID` (a **node** device of the variant), `Timeout` (duration, 1s to 1m, optional), and exactly one of
`File` {`Path` (absolute, no `.`/`..`, up to 4096 bytes), `Exists` (default true; `false` stands alone), `Contains`,
`Matches` (Go regexp), `Mode` (octal, 3-4 digits), `Owner`, `Group`} or `Custom` {`Run` (up to 8192 bytes),
`Interpreter` (absolute path), `User`, `OutputContains`, `OutputMatches` (Go regexp)}. Texts up to 1024 bytes.
`Owner`/`Group`/`User`: `^[a-z_][a-z0-9_-]{0,31}$` or a numeric id.

### Device (nodes only; a switch or hub with any of these is refused)

| Field | Limits |
|---|---|
| `Command` | string[] (up to 32 entries), replaces the image's entrypoint |
| `Args` | string[] (up to 64) |
| `System` | `linux` \| `windows` (windows only with profile `vm`); default linux |
| `Hardware` | `fast` \| `compatible` (compatible only with `vm`); default fast |
| `Terminal` | {`Enabled`, `Command` (absolute shell, default /bin/sh), `User`}; `Command`/`User` only with `Enabled` |
| `Bootstrap` | {`Timeout` (1s to 10m), `Env` (up to 64; name `^[A-Za-z_][A-Za-z0-9_]{0,63}$`, unique; value up to 4096 bytes), `Files` (up to 32), `Script` (up to 64 KiB), `Interpreter` (only with `Script`)} |

`Bootstrap.Files[]`: `Path` (absolute, unique), exactly one of `Content` (up to 64 KiB) and `Source`
(`{FileID, Name}`, an uploaded file like an attachment; `ContentHash` in responses), `Owner`, `Group`, `Mode`.
`Bootstrap.Env[].Secret` keeps the old behaviour (write-only in responses).

### Topology

`Variables`: `[{Name, Value | Template}]`, up to 64, `Name` as a task key; exactly one of `Value` (up to 1024 bytes)
and `Template`. Substitutions `{{task.<Key>}}` and `{{var.<Name>}}` work in `Bootstrap.Env[].Value`,
`Bootstrap.Files[].Content`, `Bootstrap.Script` and in the expectations of conditions; `\{{` is a literal `{{`. An
unresolved reference is an error naming the field (publish and test deploy).

## Behaviour that changed

- A **linked variant** keeps from its base everything except index, note, weight, device images and, per task,
  `Check.Answer.Values` and `Attachments`.
- **Solutions are redacted** for readers who may not see them: `Check.Answer.Values` and `Check.State.Conditions` are
  emptied (they were `Flag` and `DeviceFlagVar`).
- **Test deploy / event stands**: a task whose document needs what the laboratory does not apply yet (`Bootstrap.Files`,
  `Bootstrap.Script`, `Command`, `Args`, enabled `Terminal`, or any `Check.State` task) is refused at start with the
  error 73509 (public context `features`: what is not applied). `Bootstrap.Env` works. Template variables get a value
  per built lab (not stored per instance yet).
- A test deploy now pins a value for **every answer task** (it used to pin the device-linked ones); the author checks
  them with the same call. A text answer is compared under its options.
- The **participant task snapshot** (`Snapshot`) gets `answer`: `{type, input_hint?, ignore_case?, no_trim?}`. The
  expected value is never in it. Submissions of text answers compare under these options.
- Importing a document: `status` is optional and ignored (the import is always a draft); unknown keys, including the
  old `flag`, `linked_device`, `linked_device_id`, `device_flag_var`, `env_vars`, are errors.

## New error codes

| Code | Meaning | Public context |
|---|---|---|
| 23501 | task key invalid or repeated | `task`, `key` |
| 23502 | `Requires` invalid (unknown, self, repeat, cycle) | `task`, `key` |
| 23503 | check missing/both/another kind in a variant | `task` |
| 23504 | answer invalid | `task`, `field` (`check.answer.<key>`) |
| 23505 | state check invalid | `task`, `field` (`check.state...`) |
| 23506 | device runtime key invalid | `device`, `field` (`bootstrap.env[2].name`, `terminal.user`...) |
| 23507 | variable invalid | `field` (`variables[1].name`) |
| 23508 | substitution unresolved | `field`, `reference` |
| 23510 | resolved bootstrap names a missing device | |
| 73509 | the lab cannot apply this yet | `features` |

On import every one of the document errors also carries `path` (`versions[1].variants[2]`).
