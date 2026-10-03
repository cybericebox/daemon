# daemon

The backend of the Cyber ICE Box platform: one Go service behind `api.<domain>` that serves the HTTP API of every frontend (main, ID, admin, exercises, event) and drives the laboratory infrastructure through enrolled agents.

## What it does

- Accounts and sessions: registration, sign-in (password and Google OAuth), bot check (Cloudflare Turnstile or reCAPTCHA), roles and permissions (RBAC).
- Exercise catalog and events: exercises, challenges and flags, stands, teams, participants, scoring, submissions, event pages and forms.
- Mail and notifications: SMTP providers, templates, broadcasts, the inbox, signals and delivery journals.
- Analytics: platform and per-event analytics with PDF and CSV reports.
- Lab infrastructure: enrolls laboratory agents, deploys stands and test labs, issues lab access links and VPN configs, collects lab monitoring and traffic.
- Background jobs (River): stand launch, mail delivery, data retention, garbage collection of media and test labs, and more (`internal/jobs`).

## Architecture

Domain-driven layers; the rules are enforced by convention and by lint tools (see [CLAUDE.md](CLAUDE.md) for the full set).

| Layer | Path | Responsibility |
| --- | --- | --- |
| Model (domain) | `internal/model/*` | Entities, value objects, domain errors, every mutation as an entity method. Must not import `internal/delivery` or `internal/useCase`. |
| Use case (application) | `internal/useCase/*` | Orchestration only: fetch, call an entity method, persist. Produces the read models (views) for delivery. |
| Delivery | `internal/delivery/*` | HTTP controllers and handlers (DTOs), PostgreSQL repositories and sqlc queries, infrastructure clients (laboratory agents), middleware and protection. |
| Jobs | `internal/jobs/*` | River background workers and their registry. |
| Config, app | `internal/config`, `internal/app` | Environment parsing and validation; wiring and start-up. |
| Shared packages | `pkg/*` | Errors, pagination, password, token, secret sealing, storage, and other small libraries. |

Conventions worth knowing:

- One whole-entity repository per aggregate; sqlc rows and JSON (de)serialization stay inside it.
- Authorization is a single point: every route is gated with `RequirePermission(perm)`; `make vet` fails on a route without a gate.
- Request identity travels as one `rbac.Claims` bundle in the context.
- Time is always a parameter (`now time.Time`), so tests control the clock.

Stack: Gin, pgx/v5 with sqlc, golang-migrate, River, gomock, testcontainers, zerolog.

## Requirements

- Go 1.27 (see `go.mod`).
- PostgreSQL.
- Docker, for the integration tests (they use testcontainers and skip themselves when Docker is unavailable) and for `make sqlcGenerate`.
- The backend imports the agent contract and a few shared packages from the [laboratory](https://github.com/cybericebox/laboratory) repository. `go.mod` pins its version (there is no `replace`). To build against a checkout at `../laboratory` run `make work`, which writes `../go.work` (gitignored; CI never uses it). See [CONTRIBUTING.md](CONTRIBUTING.md) for the pin.

```
cybericebox/
  daemon/
  laboratory/
```

## Running locally

1. Start PostgreSQL (and, if you need them, an S3-compatible store and a mail catcher). The infrastructure repository has a local stand for this.
2. Create `./.env` from the table below. It is git-ignored; never commit it.
3. Run:

```bash
make run-local   # sources ./.env, then go run ./cmd/daemon
```

The daemon reads configuration from the process environment only (there is no `.env` loader in the binary), so `make run-local` sources the file first. Migrations are applied on boot with golang-migrate. In `ENV=development` they are read from `internal/delivery/repository/postgres/migrations`; in any other environment from `migrations` next to the binary.

The service answers only on the `API_HOST` host. For local work route that host to the process (for example with a hosts entry or the local edge proxy), or send the `Host` header explicitly. `GET /api/health` is a public liveness probe.

## Configuration

All settings are environment variables. Values below are placeholders; durations use Go syntax (`30s`, `24h`). A bot check is mandatory in production (`CAPTCHA_PROVIDER` other than `none`).

### General and HTTP server

| Variable | Default | Purpose |
| --- | --- | --- |
| `ENV` | `development` | `development`, `stage` or `production`. Development runs Gin in debug mode with console logs; other values run release mode with JSON logs (debug level on stage, info on production). Swagger UI is served unless `production`. |
| `MAIN_HOST` | required | Landing host (`cybericebox.com`); mail footer links and the support mailbox domain. |
| `API_HOST` | required | Host this service answers on (`api.cybericebox.com`); the OAuth redirect URI is `https://<API_HOST>/api/auth/<provider>/callback`. |
| `ID_HOST` | required | Sign-in app host: sign-in, setup, confirmation and invitation links point at it. |
| `ADMIN_HOST`, `EXERCISES_HOST` | required | Admin and exercise catalog app hosts. |
| `SUPPORT_EMAIL` | required | Default Reply-To of all mail and the contact in the mail footer (`support@cybericebox.com`). |
| `EVENT_DOMAIN` | required | Event sites are `<tag>.<EVENT_DOMAIN>`; the first labels of the hosts above that sit under it are reserved as tags. |

All six hosts are bare host names (no scheme, port or path) under one registrable domain (SameSite=Strict); the daemon refuses to start otherwise. CORS allows exactly the frontend hosts, `MAIN_HOST` and `https://*.<EVENT_DOMAIN>`. `DOMAIN` and the fixed `id`/`admin`/`exercises`/`api` subdomains are gone.

| `HTTP_SERVER_HOST` | `0.0.0.0` | Listen host. |
| `HTTP_SERVER_PORT` | `80` | Listen port. |
| `HTTP_SERVER_READ_TIMEOUT` | `10s` | Read timeout. |
| `HTTP_SERVER_WRITE_TIMEOUT` | `10s` | Write timeout. |
| `HTTP_SERVER_MAX_HEADER_MB` | `1` | Max header size in MiB. |
| `MAX_REQUEST_BODY_BYTES` | `10485760` | Cap of every request body (10 MiB); an upload route states its own larger cap. |
| `TRUSTED_PROXIES` | none | Comma-separated CIDRs or addresses of the proxies in front of the daemon (ingress, CDN). The client address is read from `X-Forwarded-For` only for requests from them; with none listed the connection address is used and the header is ignored. Behind a proxy set it, or every client shares the proxy address in the per-address limits. |
| `HTTP_SERVER_TLS_ENABLED` | `false` | Serve HTTPS. |
| `HTTP_SERVER_TLS_CERT_FILE` | `/certificates/tls.crt` | Server certificate. |
| `HTTP_SERVER_TLS_KEY_FILE` | `/certificates/tls.key` | Server key. |

### Database

| Variable | Default | Purpose |
| --- | --- | --- |
| `POSTGRES_HOST` | `localhost` | Host. |
| `POSTGRES_PORT` | `5432` | Port. |
| `POSTGRES_USER` | `postgres` | User. |
| `POSTGRES_PASSWORD` | required | Password; there is no default and the daemon does not start without it. |
| `POSTGRES_DB` | `cybericebox_dev` | Database name. |
| `POSTGRES_SSL_MODE` | `verify-full` | pgx `sslmode`. The default encrypts and checks the server certificate and name; set another mode (for example `disable` for a local development database) only for a trusted network. |

### Authentication and secrets

| Variable | Default | Purpose |
| --- | --- | --- |
| `JWT_TOKEN_SIGNATURE` | none | Signing secret of the user tokens: random, at least 32 bytes (`openssl rand -hex 32`); weaker is fatal in production, a warning elsewhere. |
| `OAUTH_STATE_SIGNATURE` | none | Signing secret of the OAuth state: random, at least 32 bytes, different from the JWT secret. With `GOOGLE_CLIENT_ID` set, an empty value is fatal in production (Google would otherwise be silently disabled). |
| `OAUTH_STATE_TTL` | `15m` | OAuth state lifetime; also the life of the OAuth state cookies. |
| `SETUP_TOKEN_TTL` | `168h` | Life of an invitation link. |
| `SIGNUP_SETUP_TOKEN_TTL` | `24h` | Life of the setup link of someone who signed up (or came through Google) by themselves. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_SECRET` | none | Google OAuth client. |
| `SUPER_ADMIN_EMAIL` | none | Email of the account that is promoted to super admin (at sign-up and at start-up; compared case-insensitively). Every start sets the role back, so a demotion made in the admin lasts only until the next restart: unset the variable to demote for good. |
| `SESSION_IDLE_TTL` | `336h` | A session unused for this long ends (slides on every use). |
| `SESSION_MAX_PER_USER` | `10` | Sessions one account keeps at once; signing in over the cap ends the oldest (0 = no cap). |
| `SESSION_ABSOLUTE_TTL` | `720h` | A session ends this long after sign-in however busy it is (also the cookie lifetime). |
| `TEMPORAL_CODE_TTL` | `1h` | Lifetime of one-time codes (confirmation, reset). |
| `PASSWORD_MIN_LENGTH`, `PASSWORD_MAX_LENGTH` | `8`, `72` | Password length bounds (bcrypt ignores bytes past 72). |
| `PASSWORD_MIN_CAPITAL_LETTERS`, `PASSWORD_MIN_SMALL_LETTERS`, `PASSWORD_MIN_DIGITS`, `PASSWORD_MIN_SPECIAL_CHARACTERS` | `1`, `1`, `1`, `0` | Complexity policy, published at `GET /api/auth/password/policy`. |
| `PLATFORM_SECRETS_KEY` | none | One 64-hex-character key (AES-256), or a keyring `id:hex,id:hex` (see below). Seals platform secrets: SMTP provider passwords, the private keys of enrolled agents and the variables of queued notifications (River job arguments carry ids only; the names, addresses and links wait in sealed rows until the mail is sent). Empty disables them and notifications cannot be queued; an invalid value is fatal. |
| `EXERCISE_SECRETS_KEY` | none | One 64-hex-character key or a keyring. Seals exercise secret env vars. Empty disables them (saving one answers 409). |
| `VPN_SECRETS_KEY` | none | One 64-hex-character key or a keyring. Seals stored VPN client configs. Empty disables their storage. |

Generate a key with `openssl rand -hex 32`. Keep every key stable: changing one makes data sealed with it unreadable.

### Bot check (CAPTCHA_PROVIDER)

One provider for the whole platform: the sign-in, sign-up and password-reset forms and the client token (below) all use it. The token travels in the JSON body as `RecaptchaToken` whichever provider issued it. A token must have been solved on a platform frontend host. The frontends take the same choice from `NEXT_PUBLIC_CAPTCHA_PROVIDER` and `NEXT_PUBLIC_CAPTCHA_SITE_KEY`.

| `CAPTCHA_PROVIDER` | Variables | Notes |
| --- | --- | --- |
| `turnstile` | `TURNSTILE_SECRET` | Cloudflare Turnstile, verified with siteverify. Free. |
| `recaptcha` (default) | classic v3: `RECAPTCHA_SECRET`; Enterprise (selected by `RECAPTCHA_PROJECT`): `RECAPTCHA_PROJECT`, `RECAPTCHA_API_KEY`, `RECAPTCHA_SITE_KEY` | Exactly one mode must be configured. |
| `none` | none | Accepts every token: local development and tests only. Fatal in production. |

Only the chosen provider's keys are required; any other value, or missing keys, stops the start. `RECAPTCHA_SCORE` (default `0.5`) is the minimum accepted reCAPTCHA score; below `0.3` (0 accepts every bot) is fatal in production.

### DoS protection (DOS_PROTECTION)

`DOS_PROTECTION=on|off` (default `off`) is independent of the provider.

- `off`: public routes share the one anonymous bucket (`RATE_LIMIT_ANON_*`, below), nothing else changes.
- `on`: on the first visit each frontend passes an invisible check of the chosen provider at `POST /api/client-token` (body `{"RecaptchaToken": "..."}`, action `clientToken`) and gets a signed `__Host-client` cookie (HttpOnly, Secure, SameSite=Lax; it is not a login and holds no personal data). Anonymous requests are then counted in a bucket per cookie. A request without a valid cookie is counted in one small shared bucket and is refused with 429 (limiter `client-token-required`, with the header `X-Client-Token: required`) unless it is the token endpoint, `/api/health`, public media or a Google sign-in redirect; the frontend answers a 429 by fetching a new token. The token endpoint is always limited by that small bucket, valid cookie or not. On top of the cookie bucket, the sign-in, sign-up and password-reset routes share a bucket of their own, and public pages of an event site (the tag of the request's `Origin`) share a bucket per event. Signed-in requests are limited per user as before. The cost with reCAPTCHA Enterprise is one assessment per new browser per day (free up to 10,000 a month); Turnstile is free.

Nothing is keyed on a client address. Every bucket is a token bucket (steady refill per minute plus a burst), in memory, per replica.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DOS_CLIENT_TOKEN_TTL` | `24h` | Life of the client cookie; the frontend fetches a new one after it. |
| `DOS_CLIENT_TOKEN_SECRET` | derived | Signing key. Empty derives one from `JWT_TOKEN_SIGNATURE`, which every replica shares. |
| `DOS_NO_TOKEN_PER_MINUTE` / `DOS_NO_TOKEN_BURST` | `600` / `300` | The shared bucket of requests without a valid cookie and of the token endpoint. Every first visit of the platform passes through it, so size it for an event start. |
| `DOS_CLIENT_PER_MINUTE` / `DOS_CLIENT_BURST` | `600` / `200` | The bucket of one client cookie. |
| `DOS_AUTH_PER_MINUTE` / `DOS_AUTH_BURST` | `1200` / `400` | The shared bucket of the sign-in, sign-up and password-reset routes. |
| `DOS_EVENT_PER_MINUTE` / `DOS_EVENT_BURST` | `6000` / `3000` | The shared bucket of the public pages of one event, per event tag. |

Refusals are recorded in the error journal as `http_429` with the limiter `no-client-token`, `client-token-required`, `client`, `auth-group` or `event`.

### Mail

**Rotating a secrets key.** Each `*_SECRETS_KEY` may be a keyring of `id:hex` entries (an id is 1 to 16 characters of `a-z`, `0-9`, `_`, `-`), for example `2026b:<hex>,2025a:<hex>`. The first key encrypts; every key opens. A ciphertext names the key that sealed it (`v1:<id>:...`), and values written before keyrings (no id) open with the keys of the ring in turn. To rotate: put the new key first and keep the old one after it; remove the old one when nothing is sealed with it any more (a value whose key is missing from the ring fails with the missing key id). Secrets are also bound to their row (a VPN config to its user and scope, an exercise secret to its variant, device and variable), so a ciphertext copied elsewhere does not open.

Sender and transport settings live in the database: SMTP providers are managed in the admin (Mail settings) and their passwords are sealed with `PLATFORM_SECRETS_KEY`. The environment transport is only a bootstrap fallback.

**Fallback rule:** providers saved in the database always win. The `SMTP_*` environment transport is used only while no provider is enabled.

| Variable | Default | Purpose |
| --- | --- | --- |
| `SMTP_HOST` | none | SMTP server. |
| `SMTP_PORT` | `587` | SMTP port. |
| `SMTP_INSECURE` | `false` | Let the env transport continue without TLS when the server does not offer STARTTLS (development mail catcher only). |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | none | SMTP credentials. |
| `SMTP_SENDER_NAME`, `SMTP_SENDER_EMAIL` | none | Default sender. |
| `SMTP_REPLY_TO_NAME`, `SMTP_REPLY_TO_EMAIL` | none | Default reply-to. |
| `SMTP_MAX_PER_SECOND` | `0` | Provider send rate limit; 0 is unlimited. |
| `SMTP_DAILY_QUOTA` | `0` | Provider daily quota; 0 is unlimited. |

### Object storage

S3-compatible store (MinIO, AWS S3) for user avatars. An empty `STORAGE_ENDPOINT` disables storage; avatar upload and serving then become unavailable.

| Variable | Default | Purpose |
| --- | --- | --- |
| `STORAGE_ENDPOINT` | none | Store endpoint. |
| `STORAGE_ACCESS_KEY`, `STORAGE_SECRET_KEY` | none | Credentials. |
| `STORAGE_BUCKET` | `cybericebox` | Bucket. |
| `STORAGE_REGION` | `us-east-1` | Region. |
| `STORAGE_USE_SSL` | `false` | Use TLS to the store. |
| `MEDIA_MAX_UPLOAD_BYTES` | `52428800` | Upload size limit (50 MiB). |
| `MEDIA_GC_GRACE` | `24h` | How long an unreferenced file is kept before garbage collection. |

### Laboratory agents

Lab infrastructure is served by one or more laboratory agents. Agents live in the database and are enrolled, never configured with certificate files.

- **In the admin** (Infrastructure, Agents): add an agent with its endpoint and the one-time enrollment token from the cluster administrator. The platform generates its own mutual-TLS key and the signing key pair of the lab access tokens; the agent signs the client certificate (the tenant identity is its CN). Both private keys are stored encrypted with `PLATFORM_SECRETS_KEY`. Certificates are renewed automatically after two thirds of their lifetime; the access key can be rotated.
- **Bootstrap from the environment**: with `AGENT_ENDPOINT` and `AGENT_ENROLLMENT_TOKEN` set and no live agent with that endpoint, the daemon enrolls it once at start-up and stores it like any other agent. The token is ignored afterwards (remove it). A failed enrollment is logged and the daemon still starts.
- Without any agent the daemon runs fine (catalog, event setup, exercise authoring); whatever needs to deploy a lab fails with `ErrInfrastructureUnavailable`.

| Variable | Default | Purpose |
| --- | --- | --- |
| `AGENT_ENDPOINT` | none | `host:443` of the agent to bootstrap. |
| `AGENT_ENROLLMENT_TOKEN` | none | One-time enrollment token (used once). |
| `AGENT_NAME` | `default` | Label of the bootstrapped agent. |
| `AGENT_CA_FILE` | none | CA of the agent's server certificate; only for a self-signed development stand (empty means system roots). |
| `AGENT_INSTANCE_ID` | `cybericebox` | Immutable platform-instance label put on every object created in the infrastructure, so two platform instances can share one cluster. Must be a valid Kubernetes label value. Never change it for a running deployment. |
| `LAB_ACCESS_TOKEN_TTL` | `1m` | How long a web access link (`/_auth`) can be opened, up to the limit the agent's proxy reports. Tokens are signed with the access key of the agent that holds the lab, never with a platform-wide key. |
| `LAB_SESSION_TTL` | `24h` | Lab web session length when the caller names no end. |

What a laboratory offers is not configured here: each agent reports it (device state persistence and its limits, the image cache, the scheduler, the lab and VPN endpoints, its certificate) and the platform keeps the last report per agent (shown in the admin agent list). The exercise editor offers state persistence when some enabled agent offers it, and a deploy is refused when the agent that holds the team's group does not. A client certificate is renewed after two thirds of its own lifetime, and a rotated-out access key stays at the agent for three times the longest link its proxy accepts (at least 15 minutes). The proxy limits (longest link, longest session) come from the agent too: a web link never promises more than the proxy allows (a warning is logged once per agent when `LAB_ACCESS_TOKEN_TTL` or `LAB_SESSION_TTL` is above them), and image prewarm skips an agent that reports its image cache is off. The agents also describe their hardware (the most CPU and memory per device, devices per lab, the sizing of a lab group's own pods: the VPN by its users, the gateway by its internet labs) and the product rules are the platform's (`RESOURCES_*` below): an agent whose device maxima are below the frame or that allows fewer than 32 devices per lab is flagged "does not meet the platform requirements" in the admin agent list (`MeetsRequirements`, `Unmet`) and is not used for planning or placement. A device carries a preset (one of the sizes above; there is no custom size) and the agent is always sent explicit, equal requests and limits; the VPN and gateway sizes of a group are rounded up to the next preset size (its CPU by the same rule; when that CPU is below what the formula needs, the next size up) before they are sent and reserved, and the largest device an agent can place is mapped down to the largest allowed size. A draft always saves and its response lists the devices outside the frame; publishing needs every device inside the frame or covered by an approved elevation (a platform admin approves per exercise the requested block or a smaller allowed one, up to the ceiling; a later version keeps the approval while every device stays at or below the approved block). The event plan (`GET /events/:id/manage/resource-plan`) counts the devices of the tasks (the largest variant of each) plus each group's VPN, sized by the event's maximum team size, and its gateway, sized by the group's internet labs, with the agents' formula; the group is created with those sizes, and an elevated task is placed only on an agent whose maxima fit it.

The variables `AGENT_TLS_*`, `AGENT_ACCESS_PRIVATE_KEY`, `AGENT_ACCESS_KEY_ID` and `LAB_ACCESS_PRIVATE_KEY` were removed and have no replacement: keys are per agent and stored in the database.

### Exercises and stands

| Variable | Default | Purpose |
| --- | --- | --- |
| `EXERCISE_FLAG_RANDOM_BYTES` | `20` | Random bytes per generated flag (1 to 1024). |
| `EXERCISE_FLAG_WARNING_BITS` | `20` | Entropy warning threshold (1 to 1024). |
| `EXERCISE_MAX_ACTIVE_TEST_DEPLOYS` | `3` | Test labs one user may run at the same time (1 to 20). |
| `EXERCISE_STAND_DEPLOY_BUDGET` | `200` | Lab deploy calls to an agent per event and pass (1 to 5000). Only protects the agent API from a burst; launch pacing is done by the laboratory operator. |
| `EXERCISE_STAND_PREWARM_LEAD` | `30m` | How long before the stand deploy time the images are prewarmed in the platform image cache; `0` turns it off (max 24h). |
| `EXERCISE_TEST_DEPLOY_TTL` / `_MAX` | `2h` / `8h` | Lease of a catalog author's test lab, and the longest it lives from its start however often extended. |
| `RESOURCES_PRESETS` | `nano=32Mi,micro=64Mi,small=128Mi,standard=256Mi,medium=512Mi,large=1Gi,xlarge=2Gi,max=4Gi` | The allowed device sizes an author picks (`id=memory`, Kubernetes quantities). The ids are translated by the frontends. Memory is binary and is the packing dimension: every size divides the next larger one, so packing leaves no hole. The CPU of a size follows from its memory, 1000m per 4Gi rounded down (32Mi gets 7m, 64Mi 15m, 1Gi 250m, 4Gi 1000m); it is not configured. A block is the smallest size, and `Blocks` counts in it. There is no custom size. |
| `RESOURCES_DEFAULT_PRESET` | `micro` | The size of a device that picked none. |
| `RESOURCES_FRAME_PRESET` | `large` | The largest size a device gets without an approval. A laboratory whose device maximum is below it, or that allows fewer than 32 devices per lab, does not meet the platform requirements and is not used. |
| `RESOURCES_CEILING_PRESET` | `max` | The largest size an approved elevation may give a device. |
| `EVENT_STAND_DEPLOY_TIMEOUT` | `20m` | A Lab the agent accepted but never reported ready fails after this. |
| `EVENT_DEFAULT_MAX_TEAM_SIZE` | `5` | Team size limit of a new event. |
| `VPN_SECRETS_KEY` | none | See above. |

### Resource calendar

The backend owns a calendar of lab resources (`internal/useCase/resourceCalendar`, `internal/model/resourceCalendar`). Reservations live only here; the agents hold none, only the tenant quota the capacity comes from.

- Time is cut into 15-minute slots. The capacity is the recorded capacity of the agents that are used (enabled, meeting the platform requirements, with a recorded capacity; no tenant quota means no limit on that resource). Feasibility is checked by packing, never by adding free room: a team (its lab group) stays whole on one agent, the fewest agents are used first, then agent priority, elevated tasks only go to agents whose device maxima fit them. Agents do not report per-node room yet, so an agent counts as one node (`PerNodeRoomReported: false`); they do not report maintenance windows either, so the calendar shows none.
- An event reservation is set by a platform admin only: size = the event's resource plan per team x teams + the buffer + the organizer's estimate for future dynamic tasks; window = from the stand deploy lead (plus `CALENDAR_LEAD_MARGIN`) to the event end + the tail gap (`CALENDAR_TAIL_GAP`, never shorter, the admin may set more). A reservation that does not fit by packing is refused (409) unless the admin allows the conflict; it is then kept, shown as not covered, and an alarm is raised. Conflicts are resolved by hand: a changed reservation keeps every team that still fits where it is, and nothing is moved automatically.
- An organizer sees allocated vs used (never an agent) and sends change requests (size, window, estimate, with a reason) that the admin approves or rejects; approving extends the existing reservation. A new task of a running event deploys only if its reservation holds it for all teams, otherwise it is refused with "not enough reserved resources, request an extension" (72508).
- Readiness alarms are real entities (`not_placed`, `agent_lost`, `agent_shrunk`, `not_connected`; the last escalates 24 h and 2 h before the deploy lead and at it). Raising one notifies the super admins through the inbox and writes the error journal (`lab_readiness`). A periodic job (`resource_calendar_check`, every minute) re-evaluates them and only adds the teams that had no agent when capacity appears.
- Test labs: a guaranteed minimum pool (an admin setting, always on, never reserved by events); above it any room no event has reserved; otherwise the author gets the nearest free window ("no free resources now, the nearest window is from HH:MM") and can book it (15 minutes to 8 hours, at most 14 days ahead, 3 at once). The booking reserves room in the calendar and covers the lab started inside it.
- Admin API (super admin, `infrastructure.read` / `infrastructure.write`): `/api/infrastructure/calendar/*` (timeline, capacity, stats, settings, alarms, change requests, event reservation). Organizer: `/api/events/:id/manage/resources`. Author: `/api/exercises/test-labs/*`. See the swagger.

| Variable | Default | Purpose |
| --- | --- | --- |
| `CALENDAR_BUFFER_PERCENT` | `0` | Optional buffer added to the size of an event reservation (0 to 200). Off by default: the agent keeps its own hidden 15% packing reserve, so a reservation is the plan x teams plus the dynamic estimate. |
| `CALENDAR_TAIL_GAP` | `1h` | Gap kept after the event end (15m to 168h); the admin may set more per event, never less. |
| `CALENDAR_LEAD_MARGIN` | `30m` | Added before the stand deploy lead: the capacity must be connected that much earlier (0 to 24h). |
| `CALENDAR_SEARCH_HORIZON` | `168h` | How far ahead the nearest free window of a test lab is looked for (1h to 2160h). |
| `CALENDAR_AGENT_FRESH` | `15m` | How recent an agent's capacity read must be for it to count as connected (6m to 24h). |
| `CALENDAR_TEST_LAB_LEASE` | `2h` | The lease a test lab is admitted for when its caller names none (15m to 24h). |

### Outgoing mail security

- An event SMTP (set by an organizer) can only name a public host: an IP literal or a name in a loopback, private, link-local (cloud metadata), carrier-grade NAT or other special range is refused when saved, and again at connect time after the name is resolved (a name that later resolves inside is never connected). Ports are limited to `SMTP_ALLOWED_PORTS` (default `25,465,587,2525`).
- The stored SMTP password is used only for the stored connection: when the host, port, username or TLS mode changes, the password has to be entered again (error 22109); a test never sends the stored password to a changed host.
- STARTTLS is required: a server that does not offer it is refused before any credential is sent. `SMTP_INSECURE=true` lifts this for the env transport only (a development mail catcher). Every send has connection and conversation deadlines. Send errors are reported as one fixed line per kind (connection failed, TLS failed, a reply code); the raw error is logged, never returned.

### Limits and timings

| Variable | Default | Purpose |
| --- | --- | --- |
| `JOB_COMPLETED_RETENTION` / `JOB_FAILED_RETENTION` | `1h` / `24h` | How long the job queue keeps finished jobs (succeeded; cancelled or discarded). Notification job arguments hold addresses, names and links. |
| `SMTP_ALLOWED_PORTS` | `25,465,587,2525` | Ports an organizer may use for an event SMTP. |
| `SSE_MAX_LIFETIME` | `30m` | Longest life of one event stream; the client reconnects. |
| `LIVE_SCREEN_LINK_MAX_TTL` | `1440h` | Cap of «until the event ends» for a live screen link. |
| `MAIL_MAX_RATE_WAIT` | `20s` | Longest a mail worker waits for its turn before the message is deferred. |
| `MAIL_QUOTA_RETRY_AFTER` | `10m` | How long a message waits when the daily quota is used. |
| `MAIL_QUOTA_RECHECK` | `30s` | How often the delivered count is re-read. |
| `MAIL_QUOTA_WINDOW` | `24h` | Window of the daily quota. |
| `MAIL_MAX_PER_SECOND_LIMIT` / `MAIL_DAILY_QUOTA_LIMIT` | `10000` / `1000000000` | Upper bounds an admin may set for a provider limit. |
| `AVATAR_MAX_BYTES` | `5242880` | Avatar size. |
| `IMAGE_MAX_PIXELS` | `16000000` | Most pixels (width times height) of an uploaded picture; a larger one is refused at upload. |
| `FLAG_ANSWER_MAX_BYTES` | `512` | Longest answer to a task that is accepted; a longer one is refused before it is stored. |
| `EVENT_LOGO_MAX_BYTES` | `2097152` | Event logo. |
| `EVENT_PREVIEW_PICTURE_MAX_BYTES` | `5242880` | Event preview picture. |
| `EVENT_CONTENT_IMAGE_MAX_BYTES` | `5242880` | Image in event page content. |
| `LIVE_LOGO_MAX_BYTES` | `1048576` | Live screen logo. |
| `EMAIL_IMAGE_UPLOAD_MAX_BYTES` | `10485760` | Raw upload of an email template image. |
| `EMAIL_IMAGE_MAX_BYTES` | `307200` | Email template image after processing. |
| `EMAIL_IMAGE_MAX_WIDTH` / `EMAIL_IMAGE_MAX_PIXELS` | `1200` / `24000000` | Width after downscale, and the decoded pixel cap. |

### Error journal

The platform error journal records what broke: 5xx, panics, 403 (with the permission that refused), 429 (with the limiter), failed job attempts and discards, a stalled or growing job queue, mail that failed after its retries, and the laboratory (an agent offline, a failed lab deploy, a certificate close to its end, component errors reported by the agent). Errors are grouped by fingerprint (kind + route template or job kind + normalized message) with a count, first and last time and a few recent samples. 404s are only counted per day: per route template for a handler 404, one counter for every unmatched path (paths are never stored). 401 is not recorded. No IP address is ever read or stored; secrets, passwords, tokens and e-mail addresses are scrubbed from messages. Only super admins see it (`platform.errors.read` / `platform.errors.write`, routes under `/api/admin/errors`).

Notifications go to the Telegram chat ids and the e-mail list that super admins keep in the journal settings (the e-mail list defaults to every super admin). A chat id the bot may not write to (it answers 403) is marked failing in the settings, never dropped. One message per fingerprint per `ERROR_JOURNAL_NOTIFY_COOLDOWN`: a storm is one message with a count.

| Variable | Default | Purpose |
| --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | none | Secret. The one Telegram bot; empty switches the Telegram channel off (chat ids are kept, nothing is sent). Never logged. |
| `ERROR_JOURNAL_SAMPLES_PER_GROUP` | `5` | Recent samples kept per error group. |
| `ERROR_JOURNAL_RETENTION` | `720h` | How long groups, samples and 404 counters are kept; a daily job deletes older ones. |
| `ERROR_JOURNAL_NOTIFY_COOLDOWN` | `15m` | Least time between two messages about one fingerprint. |
| `ERROR_JOURNAL_SPIKE_THRESHOLD` / `ERROR_JOURNAL_SPIKE_WINDOW` | `20` / `5m` | Occurrences of a known fingerprint within the window that count as a spike (messaged for 5xx and 429). |
| `ERROR_JOURNAL_BUFFER_SIZE` | `1024` | Capture queue; events beyond it are dropped (counted in the log) instead of slowing a request down. |
| `ERROR_JOURNAL_NOT_FOUND_FLUSH_INTERVAL` | `10s` | How often the in-memory 404 counters are written. |
| `ERROR_JOURNAL_QUEUE_STALL_AFTER` | `5m` | A job ready to run that waits longer than this means the workers stalled. |
| `ERROR_JOURNAL_QUEUE_BACKLOG_LIMIT` | `1000` | More waiting jobs than this is a growing queue. |
| `ERROR_JOURNAL_CERT_EXPIRY_WARN` | `336h` | A laboratory agent certificate that ends within this is reported. |
| `ERROR_JOURNAL_AGENT_OFFLINE_AFTER` | `2m` | A laboratory agent unreachable this long is reported offline. |
| `ERROR_JOURNAL_WATCH_INTERVAL` | `1m` | How often the queue and certificate checks run. |

Every request and response carries an `X-Request-ID` header (a client's own id is kept when it is 8-64 letters, digits, `.`, `_` or `-`); the journal stores it with the sample.

### Rate limits and retention

No limit is keyed on a client address (a whole on-site event sits behind one router). Mail sent by organizers and admins (invitations, broadcasts, notifications) is never limited; only mail an account triggers itself is.

| Variable | Default | Purpose |
| --- | --- | --- |
| `FLAG_RATE_LIMIT_CHALLENGE_ATTEMPTS` / `_WINDOW` | `5` / `30s` | Flag submissions per team and challenge. |
| `FLAG_RATE_LIMIT_TEAM_ATTEMPTS` / `_WINDOW` | `20` / `1m` | Flag submissions per team overall. |
| `RATE_LIMIT_USER_PER_MINUTE` / `RATE_LIMIT_USER_BURST` | `1200` / `600` | General request limiter, a token bucket per signed-in user: steady rate per minute and burst. Over it: 429 with `Retry-After`, recorded in the error journal (`http_429`, limiter `user`). Live streams and `/api/health` are exempt. In memory, per replica. |
| `RATE_LIMIT_ANON_PER_MINUTE` / `RATE_LIMIT_ANON_BURST` | `12000` / `6000` | The same bucket for ALL anonymous requests of the platform together (one global bucket, never per address), so only a real flood trips it (limiter `anonymous`). |
| `LIMIT_SIGN_IN_MAX_FAILURES` | `5` | Wrong passwords for one account (and one signed-in user's password checks) before it is locked. |
| `LIMIT_SIGN_IN_FAILURE_WINDOW` | `15m` | Window the failures are counted in. |
| `LIMIT_SIGN_IN_LOCK_BASE` / `LIMIT_SIGN_IN_LOCK_MAX` | `1m` / `15m` | First lock; it doubles up to the maximum. |
| `LIMIT_ACCOUNT_MAIL_GAP` | `1m` | Least time between two account mails of one kind (sign-up confirmation, password reset, email change) to one address. |
| `LIMIT_ACCOUNT_MAIL_PER_HOUR` | `3` | Account mails of one kind per address and hour. |
| `LIMIT_EMAIL_CHANGES_PER_HOUR` | `5` | Email-change mails one account can cause per hour. |
| `LIMIT_ACCOUNT_ACTIONS` / `LIMIT_ACCOUNT_ACTIONS_WINDOW` | `20` / `10m` | Password-change and email-change requests per signed-in user. |
| `LIMIT_STREAMS_PER_USER` | `8` | Open live-result streams per signed-in account. |
| `LIMIT_STREAMS_PER_SCREEN` | `10` | Open live-result streams per screen link. |
| `LIMIT_STREAMS_ANONYMOUS_PER_EVENT` | `500` | Open live-result streams of all anonymous readers of one event together. |
| `LIMIT_ATTEMPT_STREAMS_PER_USER` | `6` | Open attempts-journal streams per account. |
| `LIMIT_ERROR_STREAMS_PER_USER` | `3` | Open error-journal streams per super admin. |
| `LIMIT_LIVE_SCREEN_PER_MINUTE` | `120` | Requests per minute of one screen link. |
| `RETENTION_SESSION_AFTER_EXPIRY` | `2160h` | Sessions after expiry. |
| `RETENTION_LAB_TELEMETRY` | `2160h` | Lab telemetry. |
| `RETENTION_DELIVERY_LOG` | `4320h` | Mail delivery log. |
| `RETENTION_FORM_ANSWERS_AFTER_EVENT_END` | `8760h` | Form answers after an event ends. |
| `RETENTION_INACTIVE_ACCOUNT` | `26280h` | Inactive accounts. |
| `RETENTION_INACTIVITY_GRACE` | `720h` | Grace period before an inactive account is removed. |
| `RETENTION_EXPIRED_INVITATION` | `168h` | Expired invitations. |
| `RETENTION_PENDING_ACCOUNT` | `720h` | Unconfirmed accounts. |
| `RETENTION_UNUSED_ANSWER_FILE` | `24h` | Uploaded answer files never submitted. |
| `RETENTION_SIGNAL_HISTORY` | `8760h` | Signal history. |
| `RETENTION_EVENT_ANALYTICS_AFTER_END` | `8760h` | Event analytics after an event ends. |
| `RETENTION_BATCH_SIZE` | `1000` | Rows handled per retention batch. |

The retention defaults are the published periods of the Privacy Policy; change them only together with the policy text.

## Make targets

| Target | What it does |
| --- | --- |
| `make build` | Compile all packages. |
| `make vet` | `go vet` plus the lints: error codes (`lint-errors`), layer imports (`lint-layers`), route gates (`lint-routes`). |
| `make test` | Run the full test suite. Integration tests need Docker. |
| `make run` | Start the daemon with the current environment. |
| `make run-local` | Source `./.env`, then start the daemon. |
| `make sqlcGenerate` | Regenerate sqlc code (runs sqlc in Docker). Then run `go generate ./internal/delivery/repository/postgres/` for the mocks. |
| `make swagger` | Regenerate the OpenAPI spec from handler annotations. |
| `make error-catalog` | Regenerate `error-catalog/errors.en.json`. |
| `make seed` | Fill the development database with test data for live checks, or remove it (`make seed ARGS="--delete"`). See [Test data](#test-data). |
| `make tidy` | `go mod tidy`. |

## Test data

`make seed` fills the development database with data for live end-to-end checks. It drives the application's own use cases and repositories (no raw SQL), reads the same `.env` as `make run-local`, and needs the schema to be current (start the daemon once after an upgrade). It refuses to run when `ENV=production`, and when the database host is not local unless `--allow-remote` is given. It sends no mail: no signals are published.

```bash
make seed                                    # small profile, event starts in 30 minutes
make seed ARGS="--profile load --start-in 10m --reveal as_ready"
make seed ARGS="--delete --dry-run"          # list what cleanup would remove
make seed ARGS="--delete"                    # remove everything the seeder created
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--profile` | `small` | `small`: 3 teams of 3; `load`: 16 teams of 5. |
| `--start-in` | `30m` | When the event starts (at least `1m`: registration closes at the start). |
| `--duration` | `3h` | How long it lasts. |
| `--reveal` | `all_ready` | Task reveal mode: `all_ready` or `as_ready`. |
| `--event-tag` | `seedlive` | Event subdomain; it must start with `seed`. |
| `--infrastructure` | `true` | Allow dynamic labs on the event and seed the lab exercises. `false` seeds the static ones only. |
| `--credentials` | `.seed-credentials` | File for the accounts and their password. |
| `--delete`, `--dry-run` | | Cleanup; `--dry-run` only lists. |

What it creates:

- **Accounts**: two organizers (platform role `admin`; `seed-org1` owns the event, `seed-org2` is its manager) and the team members `seed-tNN-mK@seed.cybericebox.test` (member 1 is the captain). All are active and verified, with one shared password. The password is written only to the gitignored `.seed-credentials` (mode 0600, with the account list); it is never printed or logged, and a re-run keeps it.
- **Ten catalog exercises**, tagged `seed`, named `[seed] …`, published: six with a lab (one to three devices, a switch, VPN with a DHCP range, a state-persistent device, one with two per-team variants) and four static; flags are fixed (`ICE{seed_<task>}`), tags `web`, `crypto`, `forensics`, `net`, `osint`, `misc`, hints of every level. Lab devices use `nginx:alpine`, `httpd:alpine`, `redis:alpine` and `traefik/whoami`: every device must keep running by itself, so a bare `alpine` or `busybox` does not do.
- **One event** `[seed] Live Check`: team participation, open registration, public boards, published now and starting after `--start-in`; all exercises attached and shown on the board with points, hint costs, four challenge groups and prerequisites (two chains); dynamic infrastructure allowed; the teams are built, so they form at the start. The infrastructure flag cannot change after creation: delete and re-seed to flip it.

It is idempotent: a re-run finds everything by its stable name, skips what is unchanged, publishes a new version only of an exercise whose definition changed, adds missing members, and moves the start to `--start-in` from now while the event has not started. Once the event has started, the schedule and the reveal mode stay.

Cleanup removes only what carries the marker: events whose tag starts with `seed` and whose name with `[seed] `, catalog exercises tagged `seed` and named `[seed] …`, and accounts on `seed.cybericebox.test`. Events go first (an exercise cannot go while an event uses it), accounts last, deleted the way the platform deletes an account (the address is freed, sessions are revoked). An exercise that an event outside the seed uses is kept and reported.

## Database and migrations

- SQL queries live in `internal/delivery/repository/postgres/queries/*.sql`; sqlc generates the Go code next to them. Edit the SQL, run `make sqlcGenerate`, never edit generated files.
- Migrations are numbered pairs (`NNNN_name.up.sql` and `.down.sql`) in `internal/delivery/repository/postgres/migrations` and are applied automatically on boot by golang-migrate. The Docker image carries them in `/app/migrations`.
- The SQL itself is covered by testcontainers integration tests (`*_integration_test.go`), because mocks cannot catch schema drift.

## Error catalog

Every API error carries a numeric code (`FullCode = informCode*10000 + objectCode*100 + detailCode`, see `pkg/err`). The backend is the single source of truth: `error-catalog/errors.en.json` maps each code to its English message and `errors.uk.json` holds the Ukrainian text. Frontends localize by code and must not copy codes by hand.

- One error variable is one call site with a unique detail code inside its object code; `make vet` fails on duplicates.
- After adding or renaming an error run `make error-catalog`, and add the Ukrainian message.
- Changing a detail code changes the full code and breaks clients that key on it; call it out in the commit message.

## API documentation

The OpenAPI spec is generated by swag from the handler annotations into `internal/delivery/controller/http/handler/apidocs`. The Swagger UI is served at `/docs` on the API host in every environment except `production`. Regenerate the spec with `make swagger` after changing handler DTOs or annotations.

## Docker image and CI

The `Dockerfile` builds a static binary (CGO off, cross-compiled on the build platform) and copies it with the migrations into a slim Alpine image. The laboratory module comes through `go mod download` at the version `go.mod` pins, so a plain build works:

```bash
docker buildx build --build-arg LABORATORY_VERSION=$(.github/scripts/lab-pin.sh version) -t cybericebox/daemon:local .
```

The image carries the label `org.cybericebox.laboratory.commit` (the pinned laboratory version); infrastructure compares it with `LABORATORY_IMAGE_TAG` when it renders a deploy.

GitHub Actions publish multi-arch (`linux/amd64`, `linux/arm64`) images to Docker Hub as `cybericebox/daemon`. The cycle is described in [CONTRIBUTING.md](CONTRIBUTING.md):

| Workflow | Trigger | Result |
| --- | --- | --- |
| `check.yml` | PR into `develop` or `main` | tests, image build into the `buildcache` tag, nothing published; a PR into `main` needs a laboratory release in `go.mod` |
| `develop.yml` | push to `develop` | `sha-<7>` |
| `build.yml` | manual (any commit) | `sha-<7>`, not rebuilt when it exists |
| `prerelease.yml` | push to `main`, manual (major) | `vX.Y.Z-rc.N` and a GitHub pre-release |
| `promote.yml` | manual (the rc tag) | `vX.Y.Z` and `latest` on the same image, GitHub release, cleanup of rc and `sha-*` tags |

Registry credentials (`DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, with delete rights) are organisation secrets.

## Deployment

Deployment and cluster configuration: see the infrastructure repository.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).

Copyright 2026 CyberICEBox
