# Tools and coverage

Chaoxing exposes 23 operations through the CLI, local daemon and HTTP MCP. The operation registry in `internal/operation/registry.go` defines their names, arguments, schemas and required OAuth scopes; `internal/upstream` implements the source reads, activity/group discovery, work-question parsing, sign-in state machine and manual captcha state, while `internal/workdraft` implements local answer drafts.

## Calls, schemas and transport results

Business CLI commands use the running local daemon. JSON is the default; `--json` is accepted. Use `chaoxing schema assignments.list` (or `chaoxing schema list_assignments`) to inspect an operation's input and output schemas. `chaoxing capabilities` prints the local registry without an upstream read. The `get_capabilities` tool returns the same catalog as a normalized tool result.

For example, after discovering IDs with `chaoxing courses list`:

```text
chaoxing assignments list --course-id synthetic-course --class-id synthetic-class --status unknown --limit 50 --offset 0
chaoxing assignments get synthetic-course:synthetic-class:synthetic-work
chaoxing notices list --limit 20
chaoxing study overview --course-id synthetic-course
chaoxing study actions --course-id synthetic-course
chaoxing study digest --course-id synthetic-course --limit 20 --notice-limit 5 --exam-limit 10
```

The IDs above are synthetic. Replace them with values returned by the service.

`call` (daemon) and `query` (direct upstream client) accept one JSON object on stdin:

```json
{"tool":"list_assignments","arguments":{"status":"pending","limit":50,"offset":0}}
```

Successful CLI reads write `{"ok":true,"data":...}` to stdout. Exit status is 0 for complete results and 3 for partial results. Exit 3 still includes usable data on stdout; inspect `data.meta` before interpreting it. Failures write `{"ok":false,"error":...}` to stderr and exit 1. A partial result does not also emit a failure envelope.

The local daemon uses JSON success/error envelopes. HTTP MCP uses JSON-RPC and MCP envelopes: successful tool data appears in `structuredContent` and matching JSON text content. Upstream tool failures use `isError:true`, with the safe typed fault under `structuredContent.error` and as JSON text content. MCP transport/request errors remain JSON-RPC errors. Read tools require `chaoxing.read`; `sign_activity` requires `chaoxing.sign` and is advertised as non-read-only, account-bounded behavior (`openWorldHint:false`).

CLI and MCP business calls share a bounded per-account queue. Queue overflow returns a structured, retryable `RATE_LIMITED` fault; caller cancellation or deadline returns `UPSTREAM_UNAVAILABLE`. Waits consume the existing request budget. See [concurrency budgets](architecture.md#concurrency-and-resource-budgets). The CLI also classifies empty HTTP 429 responses as `RATE_LIMITED`, authorization failures as `AUTH_REQUIRED`, and other non-JSON HTTP failures as `UPSTREAM_UNAVAILABLE`. No raw response body is disclosed. Public MCP transport admission independently reports HTTP 503 with `Retry-After` when saturated.

## Operation catalog

Arguments are optional unless marked required. CLI operation IDs use two words, such as `assignments list`; MCP uses the tool names below.

| Operation ID | MCP tool | Arguments | Source and result |
| --- | --- | --- | --- |
| `connection.status` | `get_connection_status` | none | Forces a fresh course read to validate the live upstream session; returns `authenticated`, `last_login`, `automatic_login` and `fetched_at`. |
| `courses.list` | `list_courses` | none | Accessible course/class rows, wrapped in `result`, with `course_id`, `class_id`, `cpi`, name, teacher, class and `retired`. May use the two-minute course cache; does not select the current semester. |
| `assignments.list` | `list_assignments` | `course`, `course_id`, `class_id`, `status`, `limit`, `offset` | Global assignment feed without a selector; signed per-class scans with a selector. Returns `items`, source counts, pagination, states and class coverage. |
| `assignments.get` | `get_assignment` | required `id` | Known assignment summary/task page, with `assignment`, `summary`, date fields and date precision. |
| `exams.list` | `list_exams` | none | Available exam-list table: legacy raw rows in `items`, normalized `exams`, and `raw_headers`. Fields map only with matching header evidence; unmapped columns remain raw. Always partial. |
| `notices.list` | `list_notices` | `limit`, `cursor`, `include_raw` | Verified notice-list read with normalized `items`, `returned`, `next_cursor`, `has_more` and pagination metadata. Raw upstream envelopes and per-item raw fields are excluded unless `include_raw:true`. Always partial; no detail or mark-read calls. |
| `schedule.get` | `get_schedule` | `week` | Specified teaching week's upstream schedule JSON under `data`, plus `week` and `fetched_at`. Default week 1; completeness is not established. |
| `materials.list` | `list_materials` | required `course_id`, `class_id`, `cpi` | Top-level course material text under `text`. Requires an exact accessible course/class/cpi triple. Always partial; no folder traversal or downloads. |
| `study.overview` | `get_study_overview` | `course`, `course_id`, `class_id`, `limit` | Assignment-only overview of selected classes, including submitted, pending and unknown rows. Returns selected `courses`, `items`, `assignments_complete`, class coverage and unsupported sources. Always partial. |
| `study.actions` | `list_action_items` | `course`, `course_id`, `class_id`, `limit` | Source-backed assignment review items: pending rows use `review_assignment`; unknown rows use `review_state`; submitted rows are excluded. Includes assignment and provenance, with `inferred:false`. Always partial. |
| `study.digest` | `get_study_digest` | `course`, `course_id`, `class_id`, `limit`, `notice_limit`, `exam_limit` | One bounded source-backed digest combining assignment action items, normalized exam rows and trimmed notice summaries. Without a course selector assignments come from the incomplete global feed; with one they use signed class scans. Notice text is never inferred as a task. Always partial. |
| `capabilities.list` | `get_capabilities` | none | Contract version, all tool names, operation IDs/descriptions/input/output schemas, and `unsupported_domains`. Complete only for the verified tool catalog. |
| `chapters.list` | `list_chapters` | required `course_id`, `class_id`, `cpi`; optional `limit`, `offset` | Chapter catalog summaries for an exact enrolled triple, with output pagination and explicitly reported task counts. Always partial; does not enter learning pages or enumerate task cards. |
| `activities.list` | `list_activities` | `course`, `course_id`, `class_id`, `limit` | Current class activity list, including sign-in activities and the account's own sign-in state. Returns `items` with `kind`, `state`, `user_state`, `sign_type` and `signable`. |
| `works.list` | `list_work_jobs` | required `course_id`, `class_id`, `cpi` | Discovers answer-work cards under course knowledge points. Output is capped at 50 jobs; `enc`, `ktoken` and `cpi` protocol parameters stay in memory. |
| `works.questions` | `get_work_questions` | required `course_id`, `class_id`, `job_id` | Reads one editable work's questions, options, answer types and hidden form fields. Requires `chaoxing.read`; does not submit. |
| `workdrafts.upsert` | `upsert_work_draft` | required `course_id`, `class_id`, `job_id`, `question_id`, `answer` | Validates the question ID, then atomically saves or updates one local draft. Requires `chaoxing.work`. |
| `workdrafts.list` | `list_work_drafts` | required `course_id`, `class_id`, `job_id` | Lists local drafts for one work. Requires `chaoxing.work`. |
| `workdrafts.delete` | `delete_work_draft` | required `course_id`, `class_id`, `job_id`, `question_id` | Deletes one local draft. Requires `chaoxing.work`. |
| `workdrafts.export` | `export_work_draft` | required `course_id`, `class_id`, `job_id` | Exports current form data plus drafts as `pyFlag=1` save and final submit payloads. Requires `chaoxing.work`; does not POST. |
| `activities.sign` | `sign_activity` | required `course_id`, `class_id`, `activity_id`; optional `latitude`, `longitude`, `address`, `sign_code`, `qr_payload` | Submits one normal, location, QR, password or gesture sign-in, then re-reads attendance state. Password/gesture require `sign_code`; QR requires a raw scanned `qr_payload`. Requires the separate `chaoxing.sign` OAuth scope. |
| `signcaptcha.get` | `get_sign_captcha` | required `course_id`, `class_id`, `activity_id` | Returns the slider background/cutout image URLs and expiry for a challenge created by `sign_activity`. Requires `chaoxing.sign`. |
| `signcaptcha.answer` | `answer_sign_captcha` | required `course_id`, `class_id`, `activity_id`, `x_position` | Submits one slider coordinate and stores the upstream validation token for the next `sign_activity` retry. Requires `chaoxing.sign`. |

## Shared validation and metadata

All operations use the registry's shared argument validator. Arguments must match the input schema; unknown properties are rejected. Strings cannot contain control characters, required strings cannot be blank, and each field carries its own byte bound (normally 256; `upsert_work_draft.answer` allows 4096). Integers must be whole numbers within their ranges: `limit` 1..100 (default 50), `offset` 0..100000 (default 0), and `week` 1..30 (default 1). Numbers such as `latitude` (-90..90) and `longitude` (-180..180) accept JSON numbers, not strings. Booleans such as `include_raw` (default `false`) must be JSON booleans. A nonempty `class_id` requires `course_id`. `folder_id` is not an accepted argument.

Every normalized success is an object with `meta`. Legacy fields remain alongside it; array results such as courses are wrapped under `result`. The shared `operation.Result` normalizer validates the result against the registry's output schema before delivery. Its limited-schema validator checks the object, array, string, boolean, required-field and enum vocabulary emitted by the registry; it is not a general JSON Schema engine. The schema requires metadata, permits additive legacy fields, and checks declared result fields when present.

| Metadata field | Contract |
| --- | --- |
| `meta.status` | `complete` or `partial`; completeness applies to the reported scope. |
| `meta.coverage` | Object requiring boolean `complete` and string `scope`; may include per-class coverage, `sources_checked` and `sources_failed`. |
| `meta.freshness` | Object requiring string `fetched_at`, `cache_status` and `parser_revision`. Current defaults are `cache_status:"unspecified"` and `parser_revision:"2"`. |
| `meta.warnings` | Array; empty when no warnings are supplied. |
| `meta.pagination` | Optional object with `has_more`, `offset`, `total_in_source` and `next_cursor` when boolean pagination information is supplied. Some fields can be null or inapplicable. |
| `meta.trace_id` | Added by `operation.Execute` at service/direct-query entry points; a random 24-character hexadecimal request identifier, preserved through subsequent result normalization. |

`operation.Execute` resolves and validates the operation, calls the handler, normalizes/validates its result and attaches the trace. Its log records operation ID, trace ID, status and duration without arguments or response content. Direct queries log to stderr; daemon-backed calls log in the service process. CLI consumers should parse stdout for data and retain stderr separately: direct-query stderr can contain a trace log even on a successful or partial read.

Where a source provides `fetched_at`, metadata preserves it. Otherwise the normalizer supplies the current UTC time; that timestamp alone does not prove a fresh upstream read. `cache_status:"unspecified"` makes no cache-hit or cache-miss claim. Notices can have `has_more:null` when upstream supplies no verified boolean; their top-level `next_cursor` can still be present, so inspect the tool-specific fields as well as metadata.

## Course identity, states and assignment coverage

Discover exact course and class IDs with `list_courses`. `course` is a case-sensitive course-name substring. When combined with `course_id` and `class_id`, all supplied selectors must match. Selection deduplicates course/class pairs, rejects more than four selected classes with `AMBIGUOUS_COURSE`, and returns `NOT_FOUND` when no enrolled class matches. No operation infers the current semester from enrollment or the retirement marker.

Assignment IDs use `course:class:work`. Pass the `id` from `list_assignments` unchanged. Detail lookup first reads the global feed, then checks the class-scan cache or scans the matching accessible class. `NOT_FOUND` is limited to these accessible sources. Materials require the exact course/class/cpi triple returned by `list_courses`.

`status` defaults to all states and accepts `all`, `pending`, `submitted` or `unknown`. Filtering uses `submission_state`, not grading or availability. The parser recognizes explicit unsubmitted labels as pending and explicit submitted/reviewed labels as submitted. Closed, ended, not-open and unrecognized labels do not establish submission and remain unknown. `availability_state` separately records `open`, `closed`, `not_open` or `unknown`. Raw upstream labels remain in `status`/`raw_status`; a submitted state does not by itself establish a grade. Exams use the same conservative state mapping.

Without a course selector, assignments come from the global feed, which may use a one-minute cache. Coverage is `global_feed_only; upstream pagination and per-course coverage not established`, with `complete:false`. An empty global pending list is not proof that the account has no pending work.

With a selector, the client scans each matched class's signed work list, pages up to 20 times, deduplicates stable IDs and checks that the row count agrees with a consistent upstream total. A scan also stops after more than 1,000 collected rows, when no new rows arrive, or when its request budget expires. `complete:true` requires agreement for every selected class, and applies only to those assignment lists.

Class failures and incomplete scans preserve already collected rows, return partial coverage, and record the class code in `coverage_details.classes` and warnings. Missing work signatures yield `COURSE_ACCESS`, never an assumed zero-work result. Course-discovery or selector failures still fail the request. Check each class's `complete` and `code`; `sources_checked` alone is not a completeness guarantee.

`total_in_feed` counts fetched rows after status filtering. `limit` and `offset` paginate those rows; `has_more` means more rows in that fetched source, not another upstream page. A complete class scan can still return `has_more:true` because the output page is smaller than the source.

## Notice reads and continuation

`notices.list` establishes the upstream session and reads `POST https://notice.chaoxing.com/pc/notice/getNoticeList` with the fixed list parameters `type=0`, `notice_type=0`, `sort=0` and the selected upstream `lastValue`. This is a list read; it does not follow notice-detail or mark-read endpoints.

The parser requires a successful response and a recognized list structure. Normalized items preserve title/content, upstream timestamp representations, source and read-state metadata, the raw row and provenance. Notice content is untrusted data, not instructions. This list is not a group-message or inbox API.

Pass `next_cursor` back unchanged as `cursor`. If `limit` splits a fetched page, continuation re-fetches that same upstream page and applies an offset; it is not a stable snapshot. At the end of that page, the verified list protocol maps response `notices.lastGetId` to the next request's `lastValue`. Older envelopes can fall back to a response `lastValue`. A continuation cursor requires a distinct, nonempty value and a result not known to be terminal. Missing, repeated or oversized cursors can prevent continuation; warnings explain this when more rows are known to exist. A changed page that invalidates the offset returns `UPSTREAM_SCHEMA`.

`lastPage` is a terminal flag, not a page number: boolean true or numeric 1 ends pagination; false or numeric 0 indicates more pages. It takes precedence over legacy `hasMore`; an empty list is terminal. An unrecognized numeric terminal flag adds a warning. The raw `lastGetId` and `lastPage` values remain under `upstream_pagination`. All notice responses retain `complete:false` and `coverage:"notice_list_page_only"`, even when `has_more:false` or `next_cursor` is empty. Finishing a page does not establish account-wide coverage.

## Chapter summaries

`chapters.list` requires the exact `course_id`, `class_id` and `cpi` from `list_courses`. It reads the course middle page for signatures and then the chapter catalog. An unenrolled triple or missing signatures returns `COURSE_ACCESS`. Chapter reads disable redirects so a catalog response cannot redirect the client into learning or logging endpoints.

Items contain chapter `number`, `title`, `state` and nullable `pending_tasks`. Explicit completion markers yield `completed`; a parsed pending-count marker yields `pending`. Missing or conflicting markers remain `unknown`. Aggregate `completed_tasks` and `total_tasks` are nullable and come only from an explicit upstream aggregate. Missing counts are not zero.

`limit` (default 50) and `offset` (default 0) paginate the fetched catalog. The result includes `items`, `returned`, `total_in_feed`, `offset`, `has_more`, the selected triple, and `scope:"chapter_summary"`. This scope is a legacy top-level field; shared metadata falls back to `available_source_only` when no `coverage` string is supplied. `complete:false` remains explicit. Summary counts do not enumerate individual task points/cards or establish learning progress completeness.

## Class activities and sign-in state

`activities.list` reads the class activity feed at `https://mobilelearn.chaoxing.com/v2/apis/active/student/activelist` for every selected class, using `showNotStartedActive=0`. When `group_activities` is enabled, it also reads Easemob course-group history, parses only `attachmentType=15` course cards, and merges cards by stable activity ID. It projects only the fields needed to identify an activity and judge whether the account can still act: stable upstream `id`, `title`, course/class identity, `kind` (`signin` for activity types 2 and 74, otherwise `activity`), `state`, `user_state`, sign type, source (`class` or `group`) and group name.

`signable` is `true` only for an active sign-in activity whose account state is `unsigned`. It is a hint for alerting, not proof that a submission will succeed. Time values, when present upstream, are returned as UTC `starts_at`/`ends_at`; Chinese upstream times are converted from epoch milliseconds.

Class failures preserve rows from other selected classes and report per-class codes in `coverage_details.classes`; all classes failing returns the first safe fault. A group-feed failure keeps class rows and makes the result partial. `complete:true` means every selected class and the group source returned recognized data. It does not establish account-wide history: the feed is the upstream current-activity window, and `showNotStartedActive=0` excludes future activities. Pagination is output-only and `limit` defaults to 50.

`sign_type` is derived from upstream `otherId`: `normal_or_photo`, `qrcode`, `gesture`, `location`, `password` or `unknown`. It identifies the interaction type before detail is fetched, but the sign operation still re-reads activity detail before submitting.

### Sign-in submission

`activities.sign` accepts exact IDs from `list_courses` and `list_activities`. It supports only:

- normal sign-in (`otherId=0` and no photo requirement);
- location sign-in (`otherId=4` and location enabled);
- password sign-in (`otherId=5`) with a nonempty `sign_code`;
- gesture sign-in (`otherId=3`) with a nonempty `sign_code`;
- QR sign-in (`otherId=2`) with a raw scanned `qr_payload`.

The operation uses the reference Android client's protocol chain: persistent device identity, account identity, `preSign`, `analysis`, `analysis2`, then `stuSignajax`. Query order is preserved rather than alphabetically re-encoded. It sends the stable `deviceCode`, the configured Android-like user agent and the account's upstream identity. Expired sessions trigger the normal one-shot automatic login and retry; official verification requirements are not bypassed.

Location requests may supply `latitude`, `longitude` and `address`. Coordinates must lie inside the activity's published range. The service applies the reference client's independent ±0.00005-degree random offset; if upstream returns `errorLocation`, it retries once with the tightened ±0.00001 offset. Without preset coordinates, it uses the activity's published center with the same jitter. A successful submission is verified through `getAttendInfo`; a still-unsigned state returns `SIGN_REJECTED`.

Password and gesture requests first call the reference `checkSignCode` endpoint. A rejected code returns `SIGN_CODE_REJECTED` before attendance is changed. QR payloads are parsed locally in both reference formats (`SIGNIN:` and URL), checked through `signDetail`, and submitted with the QR `enc` in the reference query order. The service never fetches a QR URL. Photo and face requirements return `UNSUPPORTED`. Remote MCP callers need `chaoxing.sign`; a `chaoxing.read` token receives an insufficient-scope error before upstream is contacted.

### Slider captcha

When upstream requires a slider captcha, the first `sign_activity` call follows the reference protocol and may receive `validate_<enc2>`. If `captcha_solver` is configured, the service downloads the shade image, asks the provider for one coordinate, submits it through the same result endpoint, and retries the sign exactly once. Solver failures return `CAPTCHA_SOLVER_FAILED`; the manual flow below remains available.
### Manual slider captcha

When upstream requires a slider captcha, the first `sign_activity` call follows the reference protocol and may receive `validate_<enc2>`. The service stores only the in-memory challenge token, returns `CAPTCHA_REQUIRED`, and does not verify attendance. The caller then:

```text
get_sign_captcha -> inspect shade_image/cutout_image
answer_sign_captcha(x_position) -> sign_activity
```

`answer_sign_captcha` submits one x-coordinate in the upstream `-8..280` coordinate space and stores the returned validation token in memory. The next `sign_activity` automatically adds `enc2` and `validate` in the reference query order. A wrong answer returns `SIGN_CAPTCHA_REJECTED`; the caller may fetch a new challenge but the watcher performs at most one solver-backed retry. Challenges expire after 10 minutes and raw `enc2`, `captchaKey`, `iv`, token and validate values are never returned by the tool contract.

## Manual work questions and drafts

Use the exact course/class/cpi triple from `list_courses` with `list_work_jobs`. The operation reads the chapter catalog, then each knowledge point's card catalog, and returns only `type=workid` cards. It checks at most 50 knowledge points and returns at most 50 work jobs; a point failure makes the result partial but preserves other jobs. Protocol parameters needed by the next request are cached in memory and are not part of the public result.

`get_work_questions` requires a `job_id` from the same daemon process. If the daemon restarts, call `list_work_jobs` again to rebuild the in-memory reference. The operation reads the editable `/mooc-ans/api/work` page, recognizes graded/not-created/not-open/ended states, parses question IDs, titles, types, options and hidden form fields, and returns `answerwqbid`. Font-obfuscated pages produce a warning; titles may require manual verification.

`upsert_work_draft` validates the supplied `question_id` against that current question set before writing. Drafts are stored per account in `<state_dir>/work-drafts.json`; state writes are atomic and malformed state fails closed. `export_work_draft` re-reads the current form, combines local answers, and returns both a save payload (`pyFlag=1`) and a final submit payload (`pyFlag=""`). It does not call the upstream submission endpoint, and no tool generates answers automatically.

## Assignment overview and review items

Overview and action items use the same bounded class selection and assignment scans, with a 25-second request budget. Without selectors they consider accessible classes but still enforce the four-class bound. Their `limit` caps displayed items, not the number of upstream rows scanned; neither accepts `status` or `offset`.

Overview includes all assignment states. `assignments_complete` reports whether the selected assignment scans completed, while overall `complete` remains false. Actions exclude submitted rows and include unknown rows for review. Filtering happens before the output limit so submitted rows cannot hide pending work. Both preserve class failures and partial data.

These tools cover assignments only. They do not aggregate course exams, chapter summaries, task points or notice-derived actions, and do not claim that local coursework is complete. Use `list_exams` separately for the available exam-list page and `list_chapters` for chapter summaries. General group-message history, inbox, individual task points and folder traversal remain unsupported; activity discovery recognizes only course-group sign-in cards. Chapter summary counts do not provide a task-point API.

## Study digest

`study.digest` is a convenience aggregation for assistants that need one bounded answer. It combines verified assignment action items, exam-list rows and first-page notice summaries; it does not create a new upstream source and does not infer work from notice prose. Digest notice entries contain normalized fields and at most 240 Unicode code points of content, without the raw notice row. Exam entries use the normalized exam-list representation. Assignment entries use the same `review_assignment` and `review_state` actions as `study.actions`.

`limit` defaults to 20 and caps the combined returned item list. `notice_limit` and `exam_limit` default to 5 and 10 respectively, each bounded to 1..20. For a limit of at least 3, exams and notices each receive up to one quarter of the item budget and assignments receive the remainder, so available sections stay visible instead of being crowded out by a long assignment list. With a course selector, assignments use the signed per-class scan path and the same four-class bound as other study tools. Without a selector, assignments use the incomplete global feed and must not be treated as full account coverage. `sources` reports availability and coverage independently for assignments, exams and notices; source failures preserve the other available sections and add warnings. The overall result is always `complete:false`.

## Dates and safe errors

Preserve upstream date strings and their precision. Assignment details return `start_time` and `deadline` as parsed strings or null, `timezone:"Asia/Shanghai"`, and `date_precision:"upstream_month_day; year not inferred"`. Do not fill in a missing year. A long detail summary may be truncated and carries `summary_truncated:true`. Notice timestamps retain their upstream representation; do not assume they are normalized ISO dates.

Errors use the shared `upstream.SafeFault` contract: `code`, safe `message`, `category`, boolean `retryable`, and optional `hint`. Raw error strings, upstream messages and private URLs are not disclosed. Unknown errors or codes become `INTERNAL`, with `message:"Operation failed."`, `category:"internal"` and `retryable:false`.

`AUTH_REQUIRED`, `AUTH_COOLDOWN` and `CREDENTIALS_UNAVAILABLE` report authentication problems; follow the safe hint and complete verification on the official site when needed. `UPSTREAM_SCHEMA` means unrecognized source structure, not an empty list. `COURSE_ACCESS` and `NOT_FOUND` are accessible-source limits. `INVALID_ARGUMENT`, `INVALID_TOOL`, `INVALID_URL`, `AMBIGUOUS_COURSE` and `UNSUPPORTED` describe request or capability limits. `SIGN_REJECTED`, `SIGN_LOCATION_REJECTED`, `SIGN_EXPIRED` and `SIGN_ALREADY` describe sign-in state transitions without echoing upstream bodies. `UPSTREAM_UNAVAILABLE` and `UPSTREAM_HTTP` are marked retryable; `RESPONSE_TOO_LARGE` and `STATE_WRITE_FAILED` report resource/state failures. Class-level coverage codes such as `INCOMPLETE_SCAN` or `BUDGET_EXHAUSTED` describe partial scans rather than separate successful empty results.
