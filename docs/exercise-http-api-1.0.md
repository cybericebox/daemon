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

`Condition`: `DeviceID` (a **node** device of the variant), `Timeout` (duration, 1s to 1m, optional), and exactly one of eight
observations. All keys are optional unless marked required; a condition holds when every key that is written holds.
Texts up to 1024 bytes. `Owner`/`Group`/`User`: `^[a-z_][a-z0-9_-]{0,31}$` or a numeric id. Every text key takes the
substitutions (`{{task.<key>}}`, `{{var.<name>}}`), as listed per observation; a value substituted into a regular
expression is quoted. The built-in observations (all but `Custom`) run nothing of the device.

| Observation | Keys |
|---|---|
| `File` | `Path` (required; absolute, no `.`/`..`, up to 4096 bytes), `Exists` (default true; `false` stands alone), `Type` (`file` \| `directory` \| `symlink`; `Contains`/`Matches`/`Checksum` need `file`), `Contains`, `Matches` (Go regexp), `Checksum` (sha256, 64 hex, or a substitution), `Mode` (octal, 3-4 digits), `Owner`, `Group` |
| `Directory` | `Path` (required), `Exists` (default true; `false` stands alone), `Mode`, `Owner`, `Group`, `HasEntries[]`, `LacksEntries[]` (up to 32 names each; a name has no `/`) |
| `Process` | `Name` (no path) and/or `Matches` (regexp on the command line; at least one of the two), `User`, `Running` (default true; `false` does not combine with `User`) |
| `Port` | `Port` (required, 1-65535), `Protocol` (`tcp` \| `udp`, default tcp), `Listening` (default true; `false` does not combine with `Address`/`Process`), `Address` (an IP), `Process` (name) |
| `KernelParam` | `Name` (required; sysctl name with dots), `Equals` and/or `Matches` (at least one) |
| `Request` | `Protocol` (required: `tcp` \| `http` \| `https`), `Host` (default 127.0.0.1), `Port` (required), and for http/https only: `Method` (`GET` \| `HEAD`, default GET), `Path` (starts with `/`, default `/`), `Status` (100-599, default 200), `BodyContains`, `BodyMatches` |
| `Permission` | `User` (required), `Path` (required), `Read`, `Write`, `Execute` (at least one; each written value must equal the real answer) |
| `Custom` | `Run` (required, up to 8192 bytes), `Interpreter` (absolute path), `User` (empty = `nobody`, applied by the laboratory, never written into the stored document; any other user, root included, is the author's explicit choice and the catalog should mark it "may give these rights to the participant"), `OutputContains`, `OutputMatches` (Go regexp) |

A wrong key answers 23505 with `field` = `check.state.conditions[N].<observation>.<key>` (for example
`check.state.conditions[2].port.port`); none or several observations answer `...conditions[N].observation`. A laboratory that
does not offer an observation kind the task uses is refused with 73512 and `options` = `checks.observation.<kind>`
(`checks.observation.file.type` / `file.checksum` for those two file keys); until the laboratory contract carries the new
kinds, a laboratory reports only `file` and `custom`. Nothing is dropped silently.

### Device (nodes only; a switch or hub with any of these is refused)

| Field | Limits |
|---|---|
| `Command` | string[] (up to 32 entries), replaces the image's entrypoint |
| `Args` | string[] (up to 64) |
| `System` | `linux` \| `windows` (windows only with profile `vm`); default linux |
| `Hardware` | `fast` \| `compatible` (compatible only with `vm`); default fast |
| `Access` | the ways to reach the device in the browser, see "Access list" below (replaces `External` and `Terminal`) |
| `Bootstrap` | {`Timeout` (1s to 10m), `Env` (up to 64; name `^[A-Za-z_][A-Za-z0-9_]{0,63}$`, unique; value up to 4096 bytes), `Files` (up to 32), `Script` (up to 64 KiB), `Interpreter` (only with `Script`)} |

`Bootstrap.Files[]`: `Path` (absolute, unique), exactly one of `Content` (up to 64 KiB) and `Source`
(`{FileID, Name}`, an uploaded file like an attachment; `ContentHash` in responses), `Owner`, `Group`, `Mode`.
`Bootstrap.Env[].Secret` keeps the old behaviour (write-only in responses).

### Topology

`VPN.ReverseConnections` (bool, default false; `VPN` is the `{Enabled, DHCP, DHCPRanges, DNS}` object of the VPN gateway): devices of the
lab may start connections to the VPN clients, the participants' machines. Needs `VPN.Enabled`; set on a disabled VPN or on `Internet` it is
refused (error 23518, `field` `topology.vpn.reverse_connections`, on import also `path`). Absent in the archive and in stored exercises
means false. The test deploy status carries the effective value as `ReverseConnections` (true only with the VPN on and the key set), so the
test screen can say reverse connections are forbidden or allowed. Archive key: `topology.vpn.reverse_connections`.

**What is sent to the laboratory.** The laboratory blocks connections its devices start to VPN clients by default. The key
`spec.vpn.reverseConnections: true` goes into the lab spec only when the exercise sets `reverse_connections`, the VPN is on and the
agent reported `FeaturesResponse.vpn.reverse_connections` (an older agent rejects the key and allows everything anyway, so it never
gets it). A lab whose exercise allows the connections is not created while the features of its agent are unknown (an empty report): the
start waits like for an agent that does not answer (stands: wait `agent_unavailable`; test deploy: a retryable error), so a capable
agent never blocks what the exercise needs. An exercise that forbids them starts at once. Every lab with a VPN carries the labels
`cybericebox.com/vpn-reverse` (`1` the exercise allows, `0` forbids) and `cybericebox.com/vpn-reverse-sent` (`1` the key was
really requested).

**What is in force: `VPNReverse`** (derived from what was really requested and from the laboratory's own confirmation, `LabStatus.vpn_lab_initiated_blocked`) in the test deploy status, the stand status of organizers (`GET /events/{id}/manage/labs...`) and the
infrastructure test lab view: `blocked` (only when the laboratory confirms the block is applied by the group's VPN gateway), `allowed` (the key was sent, or an older agent whose lab the exercise wanted open), or `not_enforced` (not confirmed: an older agent, a VPN pod not rolled yet, or not applied yet; the exercise forbids the connections but the agent of that node
cannot enforce the block, so they are possible: show a warning, never "blocked"). The lab still starts, nothing is refused. Absent
for a lab without a VPN. `ReverseConnections` stays what the exercise asks for.

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
  environment path, `Command`/`Args` are sent as the service command and the `terminal` access entry as the terminal block, and the
  conditions of a `Check.State` task become laboratory checks. The refusal 73509 (public context `features`) is kept only
  for what needs a guest agent: files, script, command, args or a `terminal` access entry of a device with the profile `vm`, and a `vnc` or `rdp` access entry on any profile (itself refused
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
  contract 1.5), or a limit such as `bootstrap.max_files (at most N)`, N being what the laboratory reports (32 in the first laboratory contract), `checks.max_checks_per_lab (at most 64)`. An
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
| 23506 | device runtime key invalid | `device`, `field` (`bootstrap.env[2].name`, `access[2].port`...) |
| 23507 | variable invalid | `field` (`variables[1].name`) |
| 23508 | substitution unresolved | `field`, `reference` |
| 23518 | `reverse_connections` without the VPN network, or on the internet gateway | `field` |
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

## Access list

`Devices[].Access` replaces the keys `External` and `Terminal` (no compatibility: schema 1.0 is not released). Nodes only; a
switch or hub with any entry is refused (20917).

```
Access: [ {Kind: "http", Port: 8080, Protocol: "http"},        // Protocol http | https, empty = http
          {Kind: "terminal", Command: "/bin/sh", User: "app"}, // Command default /bin/sh, User default the image's user
          {Kind: "vnc", Port: 5900}, {Kind: "rdp", Port: 3389} ]
```

- At most one entry per kind (up to 4). Kind `http`: `Port` 1-65535 required, `Protocol` optional. Kind `terminal`: `Command`
  (absolute path) and `User` optional, no `Port`/`Protocol`; the entry being there means the console is on (there is no
  `Enabled`). Kinds `vnc` and `rdp`: `Port` required, nothing else.
- A wrong entry answers 23506 with `field` = `access[N].kind` / `.port` / `.protocol` / `.command` / `.user`.
- **Placeholder `external.link`** keeps its name and gains `Access` (`http` \| `terminal` \| `vnc` \| `rdp`, empty = `http`): the
  link points to that access entry of `DeviceName`. The device must have an entry of that kind, otherwise the placeholder is
  refused (the placeholder-node error, public context `device` and `access`); an unknown kind is a malformed placeholder.
  Resolving the link is not done by the daemon today (the editor and the board build it); `terminal` resolves to the console
  page of the device once the laboratory gives it, `vnc` and `rdp` resolve to nothing yet.
- `http` is what `External` was: the web link of a device, the `external.link` placeholder, the readiness port. A device with no
  `http` entry has no web link (the placeholder is refused as before).
- `vnc` and `rdp` are accepted and stored, but nothing serves them: the catalog shows them as unavailable, and a lab that needs
  one is refused at start with 73509 (`features` = `device web: access.vnc`).
- The `terminal` entry is sent to the laboratory as the terminal block; a laboratory without the terminal option is refused with
  73512 (`options` = `terminal`). Issuing a link to the browser terminal is not built.
- **A console is not meant for events.** Nothing bans it: `UsesConsole` (bool) is returned on the catalog card of an exercise
  (`ExerciseScope`), on the event's catalog choice (`publishedExerciseChoice`) and on the event's attached exercise, so the
  event side can show it. A ban would go next to `RequireNoStateTasks` in `loadAvailableVersion` (event_exercise.go).
- **Console link of a test lab**: `POST /exercises/deploys/{deployID}/link` takes `Access` (`http` default, or `terminal`). With
  `terminal` the link is `https://terminal.<base>/_auth?t=<token signed for the host "terminal">&next=/?lab=<lab>&device=<device>`
  (the host is the constant `labaccess.TerminalHost`, the laboratory's chart value `terminal.host`; the base domain is taken from
  the web address of any device of the lab). `Port` is ignored. 404 (33518) when the lab does not have the device or has no web
  address to say its base domain: the console is then not available. 400 for any other `Access`. The same helper
  (`labaccess.ConsoleSession`) is for the participant and moderator console links.
- Stored versions: migration 0225 turns `external` and `terminal` into `access` (a terminal that was off leaves no entry).

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

## Import into an existing exercise

The catalog frontend builds the import screens from this section. The routes are the existing ones; what is new is the
`Report` of every item (dry run and apply alike), the identity matching and the rule below.

### Rules

1. An item has two targets: **new** (`Mode: "new"`) or **into a chosen existing exercise** (`Mode: "update"` with
   `ExerciseID`). The result is always a **draft**; the published version is never touched. An import into an existing
   exercise **replaces its draft** (the draft slot is reused, or created); nothing is merged field by field.
2. A document **without `exercise.id`** is never matched to an existing exercise (not by name either): the preview
   suggests `new` and the item's `Report.Warnings` holds `no_exercise_id`. The user may still choose an existing exercise
   explicitly (`Targets` or `FixedExerciseID`); then the warning is not given.
3. **Identity is kept by matching** (see below): a matched task keeps its stored `id` and `key`, a matched device its
   `id`, so what points at them (event challenges and their recorded answers, laboratory checks, device ids in checks)
   stays valid. Items of the document that match nothing are new; stored items the document lacks are not in the draft
   any more.
4. Nothing is written by the preview.
5. Academy users in progress and events are not touched by a draft. An event keeps running the version it is pinned to
   until its organizer updates the attachment to a published version.

### Matching

| What | Matched | Notes |
|---|---|---|
| Variant | by `id`; the rest in order (the n-th unmatched variant of the document with the n-th unmatched stored one) | A variant has no name and its `index` is only decoration, so id and then order are all there is. A stored variant nothing matches is removed. |
| Task (inside the matched variant) | by `id`, then by `key` | A variant of the document without a stored counterpart looks at the first stored variant, which carries the identity of the task in every variant. A task without a key in the document gets `task_<position>` first, as everywhere, so such a document matches by position. |
| Device (inside the matched variant) | by `name` only | A renamed device is **removed and added**: it gets a new id. |
| Hint (inside a matched task) | by `id`, then by position | |

A matched task takes the **stored key**. References the document made to its own key (`Requires` of other tasks,
`{{task.<key>}}` in bootstrap values, files, script and check expectations) are rewritten to the stored key. If another
task of the document already uses the stored key, the task keeps the document's key, its item has `KeyChanged: true` and the
report has the warning `task_key_conflict`.

### Dry run: `POST /exercises/import/preview`

Body (unchanged): `{UploadID | FileID, Password?, OwnerEventID?, FixedExerciseID?, Targets?: [{ItemKey, Mode, ExerciseID?}]}`.
Without `Targets` an item whose document has an `exercise.id` of an editable exercise is *suggested* as `update`.

Each item of `Items` (existing fields as before: `ItemKey`, `Name`, `Target`, `SuggestedTarget`, `TargetExercise`,
`ConfirmationRevision`, `ReplacesDraft`, `Summary` with the counts) gets:

```json
"Report": {
  "Diff": { "Variants": [ {
      "Status": "kept | added | removed",
      "Position": 1,            "StoredPosition": 1,        // 1-based; 0 = none (added / removed)
      "MatchedBy": "id | order",
      "Tasks":   { "Kept":    [ {"Name": "Web", "PreviousName": "Web task", "Key": "web", "ID": "…", "MatchedBy": "id | key", "KeyChanged": false} ],
                   "Added":   [ {"Name": "New task", "Key": "fresh"} ],
                   "Removed": [ {"Name": "Old task", "Key": "old", "ID": "…"} ] },
      "Devices": { "Kept":    [ {"Name": "srv", "ID": "…", "MatchedBy": "name"} ],
                   "Added":   [ {"Name": "database"} ],
                   "Removed": [ {"Name": "db", "ID": "…"} ] } } ] },
  "Issues": [ {"Code": 23504, "Message": "…", "Path": "variants[2].topology.devices[1]", "Context": {"task": "…", "field": "…"}} ],
  "Publishable": false,
  "Warnings": [ {"Code": "used_by_events", "Context": {"events": 2}} ],
  "Usage": { "Events": [ { "EventID": "…", "Name": "…", "Archived": false, "VersionPublished": true, "RunningLabs": 3,
                           "Removed": [ {"Name": "Old task", "Attempts": 12, "Solves": 4} ],
                           "Added":   [ {"Name": "New task"} ],
                           "VariantMissing": false, "Blocked": true } ],
             "TestDeploys": 0 }
}
```

- `Diff.Variants` lists the variants of the document in order (`kept`/`added`), then the stored variants that go
  (`removed`, with all their tasks and devices as `Removed`). For an item that creates a new exercise everything is `added`.
  The slices are never `null`. Tasks of an `added` variant that match a stored task are listed as `Kept` (they take its
  identity). `ID` is the stored id; an added item gets its id when the draft is written.
- `Issues` are the errors of the result: the whole version's publish errors, then each variant's own, then the platform
  gates (node profiles, infrastructure, resources). Each has the public `Code` and `Message` of the error, the document
  `Path` of what it names (the variant is 1-based in the imported version; empty when it names the whole version) and the
  public `Context` the error carries (`task`, `device`, `field`, ...). Errors of the document itself (a broken key, an unknown
  field) refuse the request instead, as before, with `path`. `Publishable` is true exactly when `Issues` is empty. A draft
  with issues is still imported.
- `Warnings` never forbid anything. Codes: `no_exercise_id`, `task_key_conflict` (`task`, `key`), `used_by_events`
  (`events`), `removed_task_has_attempts` (`event`, `task`, `attempts`, `solves`), `running_labs` (`labs`: running event
  laboratories plus leased test labs of the exercise).
- `Usage` is present for an update (empty for a new exercise). `Events` are the **active attachments** of the exercise in
  events (archived events included, `Archived`). `VersionPublished` tells whether the event is pinned to the version
  published now. `Removed` / `Added` are the tasks of the event's variant (its fixed variant, else the first) that would go
  and come once the draft is **published and the organizer moves the event to it**, by the event's own rule (task id, then
  name). `Attempts` / `Solves` are the answers recorded on the removed task's challenge in that event (solves: teams with a
  correct, non-practice answer). `Blocked`: moving that event would be refused, because a removed task has recorded answers
  or the event's fixed variant would not exist (`VariantMissing`).

### Apply: `POST /exercises/import/uploaded` and `POST /exercises/import/upload`

Unchanged request (`Targets`, `FixedExerciseID`, `ConfirmedUpdates: [{ItemKey, Revision, ReplaceDraft}]`; every update
needs the preview's `ConfirmationRevision` and `ReplaceDraft: true` when the exercise has a draft). The response
`{Exercises, Results, Created, Updated, Unchanged}`: every element of `Results` is `{ItemKey, Mode, ExerciseID, Report}`
with the **same `Report`** the dry run showed (`Mode` is `new`, `update` or `unchanged`; `unchanged` leaves the working
copy as it is). A background import (device image files, `202 {JobID}`) carries the same `Results[].Report` in
`GET /exercises/import/jobs/{jobID}` when it is done.

### Errors

Existing import errors, unchanged: 22901 targets do not match the items, 22902 one exercise from several items, 22903
exactly one source, 22904 in-task import needs one item and its fixed target, 22905 confirm the update and the draft
replacement, 22908 an update needs the immutable source route, 72906 the source or target changed (preview again),
20990 unsupported schema (expects `cib-exercise/1.0`). No new error codes: the report's `Issues` reuse the codes of the
publish checks (23501 to 23513 and the older ones, see above).

## What the laboratories can run: `GET /exercises/capabilities` -> `LabCapabilities`

Readable by every catalog author. The editor uses it to disable an option with a reason instead of letting the author meet
73512 or 73509 at the start of a test lab. It names no agent and carries no address, version or count (the daemon is a lab
client: only what it can act on).

```
LabCapabilities: {
  Known: true,
  Observations: {file, directory, process, port, kernel_param, request, permission, custom, "file.type", "file.checksum"},
  Access:       {http, terminal, vnc, rdp},
  Profiles:     {container, microvm, vm},
  Bootstrap: "all", Checks: "all", VPNReverse: "all"
}      // every value is one of "all" | "some" | "none" | "unknown"
```

- **Aggregation rule.** Over the agents that are used (enabled, meeting the platform requirements, compatible), per option:
  `all` every agent that has reported can; `none` no agent can; `some` otherwise (warn). An agent that has not reported yet
  may be able to, so with one still pending `all` becomes `some` and `none` becomes `unknown`. Neither the union nor the
  intersection alone: the three words carry both ("can this exercise start somewhere" is `some` or `all`; "everywhere" is `all`).
- **Nothing reported yet** (or no infrastructure): `Known: false` and every value `unknown`, never `false`/`none`. The editor
  neither warns nor disables on `unknown`.
- **Meaning per key.** `Observations`: the kinds of a state check the checker runs, plus the two file keys; an agent that lists
  none is a first-generation checker and runs `file` and `custom` only. `Access`: `http` is always served, `terminal` when the
  terminal option is on, `vnc` and `rdp` are always `none` (accepted by the schema, not served). `Profiles`: the platform
  setting «Профілі вузлів» (`all` when ticked, else `none`), the same for every laboratory. `Bootstrap`: device initialization
  (files, script) and the service command. `Checks`: state checks are switched on. `VPNReverse`: `topology.vpn.reverse_connections`
  can be enforced.
- **Caching and refresh.** The block is computed on each request from the reports the daemon already keeps (no call to an agent,
  so no cost and no stale copy of its own). A report is refreshed by the monitoring and again when an agent refuses a lab, so a
  switched-on option shows up after the next refresh of that agent's report.

## The working copy as a document: `GET /exercises/{id}/draft/document`

Same access as `GET /exercises/{id}/draft`. Returns the working copy (the draft, else the published content, else an empty copy)
as the exercise document of the archive: schema `cib-exercise/1.0`, one version, keys in the order of the specification, files
as their archive paths (`files/...`, the same layout the export gives; no bytes), a platform image by its reference.

- Response: `200`, `Content-Type: application/yaml; charset=utf-8`, the YAML itself (not wrapped in the `Data` envelope),
  `Cache-Control: private, no-store`, `X-Content-Type-Options: nosniff`. At most 8 MiB; a larger document answers `413`
  (the archive-too-large error), never a cut one.
- Secret env values are never in it (empty, `secret: true`). A reader who may not see flags gets no answer values and no
  conditions, and no internal notes, as `GET .../draft` does.
- It is a view: nothing is written. No `?variant=` (a linked variant cannot stand without its base): the whole document.
- Errors as the draft read: `404` for an unknown exercise, `403` without read access.

## Substitutions are not allowed in the description or a hint

`{{task.<key>}}` and `{{var.<name>}}` (and a malformed one) in a task's `Description` (any text of the rich-text document) or
in a hint's `Text` are refused with 23519, public context `task` and `field` (`description` or `hints[N].text`): the participant
reads these texts, so an answer or an infrastructure value would be shown to everyone. Only the task's own placeholders
(`{{key}}`) belong there; `\{{` writes a literal `{{`. Checked on import and on publish, not on every autosave of the draft.
