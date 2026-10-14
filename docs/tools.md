# Tools and coverage

Chaoxing exposes 13 read-only operations through the CLI, local daemon and HTTP MCP. The operation registry in `internal/operation/registry.go` defines their names, arguments and schemas; `internal/upstream/data.go` and `internal/upstream/chapters.go` implement the source reads and coverage rules.

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

The local daemon uses JSON success/error envelopes. HTTP MCP uses JSON-RPC and MCP envelopes: successful tool data appears in `structuredContent` and matching JSON text content. Upstream tool failures use `isError:true`, with the safe typed fault under `structuredContent.error` and as JSON text content. MCP transport/request errors remain JSON-RPC errors. All MCP tools require `chaoxing.read` and advertise read-only, non-destructive, idempotent, account-bounded behavior (`openWorldHint:false`).

## Operation catalog

Arguments are optional unless marked required. CLI operation IDs use two words, such as `assignments list`; MCP uses the tool names below.

| Operation ID | MCP tool | Arguments | Source and result |
| --- | --- | --- | --- |
| `connection.status` | `get_connection_status` | none | Forces a fresh course read to validate the live upstream session; returns `authenticated`, `last_login`, `automatic_login` and `fetched_at`. |
| `courses.list` | `list_courses` | none | Accessible course/class rows, wrapped in `result`, with `course_id`, `class_id`, `cpi`, name, teacher, class and `retired`. May use the two-minute course cache; does not select the current semester. |
| `assignments.list` | `list_assignments` | `course`, `course_id`, `class_id`, `status`, `limit`, `offset` | Global assignment feed without a selector; signed per-class scans with a selector. Returns `items`, source counts, pagination, states and class coverage. |
| `assignments.get` | `get_assignment` | required `id` | Known assignment summary/task page, with `assignment`, `summary`, date fields and date precision. Never follows start-answer links. |
| `exams.list` | `list_exams` | none | Available exam-list table: legacy raw rows in `items`, normalized `exams`, and `raw_headers`. Fields map only with matching header evidence; unmapped columns remain raw. Always partial. |
| `notices.list` | `list_notices` | `limit`, `cursor` | Verified notice-list read with normalized `items`, raw `data`, `returned`, `next_cursor`, `has_more` and raw upstream pagination metadata. Always partial; no detail or mark-read calls. |
| `schedule.get` | `get_schedule` | `week` | Specified teaching week's upstream schedule JSON under `data`, plus `week` and `fetched_at`. Default week 1; completeness is not established. |
| `materials.list` | `list_materials` | required `course_id`, `class_id`, `cpi` | Top-level course material text under `text`. Requires an exact accessible course/class/cpi triple. Always partial; no folder traversal or downloads. |
| `study.overview` | `get_study_overview` | `course`, `course_id`, `class_id`, `limit` | Assignment-only overview of selected classes, including submitted, pending and unknown rows. Returns selected `courses`, `items`, `assignments_complete`, class coverage and unsupported sources. Always partial. |
| `study.actions` | `list_action_items` | `course`, `course_id`, `class_id`, `limit` | Source-backed assignment review items: pending rows use `review_assignment`; unknown rows use `review_state`; submitted rows are excluded. Includes assignment and provenance, with `inferred:false`. Always partial. |
| `study.digest` | `get_study_digest` | `course`, `course_id`, `class_id`, `limit`, `notice_limit`, `exam_limit` | One bounded source-backed digest combining assignment action items, normalized exam rows and trimmed notice summaries. Without a course selector assignments come from the incomplete global feed; with one they use signed class scans. Notice text is never inferred as a task. Always partial. |
| `capabilities.list` | `get_capabilities` | none | Contract version, all tool names, operation IDs/descriptions/input/output schemas, and `unsupported_domains`. Complete only for the verified tool catalog. |
| `chapters.list` | `list_chapters` | required `course_id`, `class_id`, `cpi`; optional `limit`, `offset` | Chapter catalog summaries for an exact enrolled triple, with output pagination and explicitly reported task counts. Always partial; does not enter learning pages or enumerate task cards. |

## Shared validation and metadata

All reads use the registry's shared argument validator. Arguments must match the input schema; unknown properties are rejected. Strings are limited to 256 bytes by the validator, cannot contain control characters, and required strings cannot be blank. Integers must be whole numbers within their ranges: `limit` 1..100 (default 50), `offset` 0..100000 (default 0), and `week` 1..30 (default 1). A nonempty `class_id` requires `course_id`. `folder_id` is not an accepted argument.

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

## Assignment overview and review items

Overview and action items use the same bounded class selection and assignment scans, with a 25-second request budget. Without selectors they consider accessible classes but still enforce the four-class bound. Their `limit` caps displayed items, not the number of upstream rows scanned; neither accepts `status` or `offset`.

Overview includes all assignment states. `assignments_complete` reports whether the selected assignment scans completed, while overall `complete` remains false. Actions exclude submitted rows and include unknown rows for review. Filtering happens before the output limit so submitted rows cannot hide pending work. Both preserve class failures and partial data.

These tools cover assignments only. They do not aggregate course exams, chapter summaries, task points or notice-derived actions, and do not claim that local coursework is complete. Use `list_exams` separately for the available exam-list page and `list_chapters` for chapter summaries. Group messages, inbox, individual task points, exam start, submission, attendance and folder traversal remain unsupported; chapter summary counts do not provide a task-point API.

## Study digest

`study.digest` is a convenience aggregation for assistants that need one bounded answer. It combines verified assignment action items, exam-list rows and first-page notice summaries; it does not create a new upstream source and does not infer work from notice prose. Digest notice entries contain normalized fields and at most 240 Unicode code points of content, without the raw notice row. Exam entries use the normalized exam-list representation. Assignment entries use the same `review_assignment` and `review_state` actions as `study.actions`.

`limit` defaults to 20 and caps the combined returned item list. `notice_limit` and `exam_limit` default to 5 and 10 respectively, each bounded to 1..20. For a limit of at least 3, exams and notices each receive up to one quarter of the item budget and assignments receive the remainder, so available sections stay visible instead of being crowded out by a long assignment list. With a course selector, assignments use the signed per-class scan path and the same four-class bound as other study tools. Without a selector, assignments use the incomplete global feed and must not be treated as full account coverage. `sources` reports availability and coverage independently for assignments, exams and notices; source failures preserve the other available sections and add warnings. The overall result is always `complete:false`.

## Dates and safe errors

Preserve upstream date strings and their precision. Assignment details return `start_time` and `deadline` as parsed strings or null, `timezone:"Asia/Shanghai"`, and `date_precision:"upstream_month_day; year not inferred"`. Do not fill in a missing year. A long detail summary may be truncated and carries `summary_truncated:true`. Notice timestamps retain their upstream representation; do not assume they are normalized ISO dates.

Errors use the shared `upstream.SafeFault` contract: `code`, safe `message`, `category`, boolean `retryable`, and optional `hint`. Raw error strings, upstream messages and private URLs are not disclosed. Unknown errors or codes become `INTERNAL`, with `message:"Operation failed."`, `category:"internal"` and `retryable:false`.

`AUTH_REQUIRED`, `AUTH_COOLDOWN` and `CREDENTIALS_UNAVAILABLE` report authentication problems; follow the safe hint and complete verification on the official site when needed. `UPSTREAM_SCHEMA` means unrecognized source structure, not an empty list. `COURSE_ACCESS` and `NOT_FOUND` are accessible-source limits. `INVALID_ARGUMENT`, `INVALID_TOOL`, `INVALID_URL`, `AMBIGUOUS_COURSE` and `UNSUPPORTED` describe request or capability limits. `UPSTREAM_UNAVAILABLE` and `UPSTREAM_HTTP` are marked retryable; `RESPONSE_TOO_LARGE` and `STATE_WRITE_FAILED` report resource/state failures. Class-level coverage codes such as `INCOMPLETE_SCAN` or `BUDGET_EXHAUSTED` describe partial scans rather than separate successful empty results.
