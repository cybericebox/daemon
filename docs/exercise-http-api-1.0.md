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
| `Requires` | string[] | Keys of tasks of the same variant that must be done first. At most 32; no repeats, no self reference, no cycles, every key must exist. In an event the keys become the prerequisites of the board (see "Locks by `Requires`"). |
| `Check` | object | `{Answer}` or `{State}`: exactly one (a draft may have neither; publishing needs one; the kind and the answer type are the same in every variant). |

`Check.Answer` (the participant types a value):

| Field | Type | Limits |
|---|---|---|
| `Type` | `flag` \| `text` | required |
| `Values` | string[] | flag: each `ICE{...}`, or `template:ICE{...}` (a template may repeat a class with `{n}`, 1-64: `template:ICE{[a-f0-9]{16}}`; the generated flag never holds `{` or `}` inside); none = a random flag per instance. text: required, 1-100 values, each 1-256 characters, no control characters, `template:<pattern>` allowed (a pattern with at least one random position, classes `[a-z0-9]`, `\d \l \u`, repeat `{n}` 1-64). Without delivery into a lab (the task's `{{task.<Key>}}` referenced by a bootstrap) a task has exactly one fixed value at publish. |
| `InputHint` | string | Shown inside the answer field; at most 100 characters, no control characters. Both types. Reaches participants in the pinned task snapshot as `Answer.input_hint`. |
| `IgnoreCase` | bool | text only (an error on flag) |
| `Trim` | bool \| null | text only; absent = on. An error on flag. |

`Check.State` (the lab checks devices; Academy only: refused when the task is added to an event, see "State tasks never join an event"):

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
- **Test deploy / event stands** apply the whole runtime of the document (laboratory contract 1.5): the resolved
  `Bootstrap.Files` and `Bootstrap.Script` (with `Interpreter` and `Timeout`) travel in the laboratory's bootstrap message
  (a file that names a file of the archive is read from the file store, at most 256 KiB), `Bootstrap.Env` stays on the
  environment path, `Command`/`Args`/`Terminal` are sent as the service command and the terminal block, and the
  conditions of a `Check.State` task become laboratory checks. The refusal 73509 (public context `features`) is kept only
  for what needs a guest agent: files, script, command, args or terminal of a device with the profile `vm` (itself refused
  where a lab is deployed), and a state task that observes such a device.
- **What the laboratory is told to run after a bootstrap.** A device with a bootstrap list (files or script) or with its own
  `Command`/`Args` needs the service command: the author's `Command`/`Args`, else the image's entrypoint and cmd (an
  author command replaces both; `Args` alone replace the image's cmd; an image without an entrypoint runs its cmd). The
  user and the working directory are the image's. When neither the author nor the image gives a command the start is
  refused with the error 73511 naming the device (`device`).
- **Where the image configuration comes from.** The start configuration (entrypoint, cmd, user, working directory) of an
  image of the platform registry (`platform:<name>@sha256:...`) is read from its config blob when the image is published
  (archive import and the browser upload alike) and stored by manifest digest; the deploy looks it up. An image given
  by reference to an external registry (`ghcr.io/...`) has none: no mechanism of the daemon reads the config of an external
  image (it only checks that the manifest exists), so such a device needs its own `Command` when it has a bootstrap list.
- **Laboratory options.** A task that needs an option the target laboratory does not offer is refused at start with the
  error 73512 whose public context `options` names it: `checks` (a state task), `terminal`, `bootstrap` (an agent older than
  contract 1.5), or a limit such as `bootstrap.max_files (at most 8)`, `checks.max_checks_per_lab (at most 64)`. An
  agent that has not reported yet is not second-guessed.
- **Variables of the infrastructure are pinned.** A template variable is drawn once per team's stand of an event exercise
  (table `stand_variables`) and the same value goes into every rebuilt, recreated or reset lab (the bootstrap and the
  expectations of the checks). A test lab is built once and keeps none.
- **State tasks never join an event.** Attaching a version with a `Check.State` task to an event, updating an attached
  exercise to such a version and reverting to one are refused with the error 23513 (public context `task`: the first such
  task). Previewing a catalog exercise is not refused.
- **Locks by `Requires`.** When a task joins an event (and when a newer version changes a task's `Requires`) its keys become
  the prerequisites of its event challenge, the board's own lock: a team's challenge with an unsolved prerequisite has
  `Locked` true in the participant board (`GET /events/{id}/teams/challenges/mine`), shows only name and difficulty and
  lists the names of what it waits for in `Prerequisites` (`Name`, `Solved`); it accepts no answer (the existing
  prerequisite error) and no manual check (error 73514, public context `waits_for`: the names of the unsolved prerequisites,
  semicolon separated). The organizers may change the relations afterwards (`PUT .../relations`); the document sets them, it
  does not pin them.
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
| 23513 | a task with `Check.State` cannot join an event | `task` |
| 73509 | the lab cannot apply this (a vm-profile device needs a guest agent) | `features` |
| 73511 | the command to start after the bootstrap of a device is unknown | `device` |
| 73512 | the laboratory lacks an option or a size the task needs | `options` |
| 73514 | the task waits for other tasks | `waits_for` |
| 73515 | the task does not allow a manual check, or is not a state task | |
| 73516 | the manual check was asked for too often (HTTP 429, `Retry-After`) | |

On import every one of the document errors also carries `path` (`versions[1].variants[2]`).

## Results of laboratory checks

Every laboratory check of a state task has an id `"<task key>:<n>"` (n: the position of the device among the devices of the
task's conditions, from 1): one check per (task, device), carrying all the task's conditions on that device. The id maps
back to the task. The laboratory publishes only changes in its Monitoring stream; the daemon records each result per lab
(table `lab_check_results`): passed and not passed are stored, `first_passed_at` never goes back, an error ("could not be
made") keeps the stored state and only refreshes the detail, an unknown state and ids of other makers are ignored.

A task is **solved** when all its checks are: mode `once`: every check has passed at least once (it stays solved, at the
moment the last of them first passed); mode `continuous`: every check passes now (it may go back to not solved; `SolvedAt` is
when the current pass began and `FirstSolvedAt` the first time every check had passed, kept after it goes back).
`Detail` and `Value` of a check are for the author and the teacher only.

Schedule and time limit: the time limit of a check is its longest condition `Timeout`, else 30 s (capped by a shorter
`Interval`); a schedule is never shorter than the time limit (an explicit timeout longer than the `Interval` raises the
schedule to it).

### Test deploy status: `GET /exercises/deploys/{deployID}`

New fields (author of the deploy only):

| Field | Meaning |
|---|---|
| `StateTasks[]` | one per state task: `TaskID`, `Key`, `Mode`, `Manual`, `Solved`, `SolvedAt`, `FirstSolvedAt`, `Known` (every check has a result), `Checks[]` {`ID`, `Known`, `Passed`, `FirstPassedAt`, `ChangedAt`, `Value`, `Detail`} |
| `SolvedTaskIDs` | now also holds the state tasks that are solved |
| `Devices[].Bootstrap` | `{Phase: pending|done|failed, Attempts, Reason}`; absent for a device without a bootstrap |

The same `Devices[].Bootstrap` is in the stand status that organizers and administrators read (`labview`); a participant never
gets it, nor `Detail`, expected values or the definition of a check.

### Manual run

- `POST /exercises/deploys/{deployID}/tasks/{taskID}/run-checks`: the author asks for a check of one state task of their
  own test lab now. Answer `{Asked: true}`: the request was recorded for the laboratory, the result comes with the next
  status. A task that is not a state task or does not allow `Manual` answers 73515.
- `POST /events/{id}/teams/challenges/{challengeID}/run-checks`: a participant asks for a check of their own team's task.
  Same answer; all the board's locks apply (published, stage, prerequisites: 73514 with `waits_for`); the answer never carries
  anything of what is checked. Reachable only for a task that has a state check, which an event does not take today.

Both are limited to 3 asks a minute per lab group (a team; the author's own group for a test) and task: the next one answers
429 with `Retry-After` (73516). A laboratory with the checks option off refuses with 73512 (`options: checks`).

### Agents: `GET /infrastructure/agents`

`Features` gets `ChecksEnabled`, `ChecksMaxPerLab`, `TerminalEnabled` and `BootstrapAvailable` (false for an agent older than
contract 1.5), so the catalog can tell whether a state task can start there.

## Terminal

The terminal block is sent to the laboratory (`terminal {enabled, command[], user}` of the device). Issuing a link to the
browser terminal is not built: the laboratory's terminal component and the proxy route for it do not exist yet; the
link will be issued next to `OpenTestDeployLink` / the participant lab link (`ExerciseUseCase.OpenTestDeployLink`,
`EventUseCase` lab link) once they do.

## What an image itself runs: `POST /exercises/{id}/images/service`

For the device editor: «Команда», arguments and terminal user show the image's own values as the default (placeholder), so the
catalog needs entrypoint, cmd, user and working dir by image reference. Same access as saving the draft, same body as
`images/check`; no registry call, the answer is immediate (a stored value is read).

```
POST /exercises/{id}/images/service   {"Images": ["platform:web@sha256:…", "nginx:1"]}
-> [{"Image": "platform:web@sha256:…", "Known": true,  "Entrypoint": ["/entry.sh"], "Cmd": ["serve"], "User": "www", "WorkingDir": "/srv"},
    {"Image": "nginx:1",               "Known": false, "Entrypoint": [], "Cmd": [], "User": "", "WorkingDir": ""}]
```

- One entry per distinct non-empty image, in the order sent (at most 200, each at most 512 characters). Arrays are never null.
- Use it for a stored device (send its `Image`) and for an image the author just typed or uploaded (an upload returns the
  platform reference; its configuration is stored when the image is published, so it is `Known` right away).
- `Known: false` (the other fields empty) means the daemon has no start configuration: an image by reference to an external
  registry (nothing reads its config, it is not pulled at editing time), a platform image published before the configuration was
  kept, or a failed read. The editor then has no default to show. A device with bootstrap files or a script needs its own
  `Command` in that case, otherwise the start refuses with the device named.
- The device DTO is unchanged: the values are not stored in the exercise, and a device without `Command` keeps meaning «what
  the image runs».

## Size of bootstrap files

A device's bootstrap is refused when a file is over 256 KiB (`MaxBootstrapFileBytes`, inline text or a file of the archive
named by `source`) or when the script and all files of the device are over 512 KiB together (`MaxBootstrapDeviceBytes`).
The same two numbers are checked when a lab starts (the start reads the file with the same cap), so a file that was saved
also starts.

- When: at a draft save (`PUT /exercises/{id}/draft`) by the stored size of the file, and at import by the bytes in the archive.
- Answer: 400, code 23517. Public context: `device` (name), `field` (`bootstrap.files[N].source`, `bootstrap.files[N].content` is
  not reachable: inline text is capped at 64 KiB by its own rule; or `bootstrap` for the device total), `file` (the file's
  path in the device, absent for the device total) and `limit` in bytes. At import `path` is also there, the place of the
  variant in the document (`versions[1].variants[1]`).
- The editor can show the size of a chosen file and refuse early with the same numbers; the server check is the final one.
