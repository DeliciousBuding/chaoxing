# Tools and coverage

`call` and `query` accept one JSON object on stdin:

```json
{"tool":"list_assignments","arguments":{"status":"pending","limit":50,"offset":0}}
```

Successful CLI responses use `{"ok":true,"data":...}`. Direct `query` errors use `{"ok":false,"error":{"code":"...","message":"..."}}`; the daemon's `call` path currently emits an error string such as `AUTH_REQUIRED: Login required.`. HTTP MCP uses MCP/JSON-RPC envelopes around the same read-only tool results. These are different transport formats.

| Tool | Arguments | Current behavior |
| --- | --- | --- |
| `get_connection_status` | none | Read courses to check the session; a cached read may not be a fresh network probe |
| `list_courses` | none | Accessible courses with `course_id`, `class_id`, `cpi` and retirement marker |
| `list_assignments` | `course`, `status`, `limit`, `offset` | Global feed without a course filter; signed, paginated matching-class scans with a course filter |
| `get_assignment` | required `id` | Read a summary/task page from a known feed or a matching accessible course scan; never follow start-answer links |
| `list_exams` | none | Parse the upstream exam list; full account coverage is not established |
| `list_notices` | none | Read a notice page; current output is incomplete |
| `get_schedule` | `week`, default 1, range 1..30 | Read a week's schedule |
| `list_materials` | required `course_id`, `class_id`, `cpi` | Read the course's top-level materials page; no downloads or folder traversal |

Strings are at most 256 characters at the MCP boundary. Assignment `course` is a course-name substring, not a course ID. `status` is `all`, `pending` or `submitted`; `pending` recognizes the current upstream pending labels, and `submitted` currently means rows not recognized as pending, not proof of grading. `limit` defaults to 50 (1..100); `offset` defaults to 0 (0..100000 at the MCP boundary).

Assignment IDs retain the stable `course:class:work` format: pass the `id` returned by `list_assignments` unchanged. Detail lookup can scan the matching accessible class when the ID is absent from the global feed or course-scan cache. Materials require the exact course/class/cpi triple returned by `list_courses`. The MCP schema accepts `folder_id`, but the core rejects nonempty values as `UNSUPPORTED`.

## Assignment coverage

Without `course`, the core fetches the upstream global feed. Account-wide upstream pagination and exhaustive coverage remain unverified and pending a global coverage fix. Responses carry `complete: false` and a `coverage` marker beginning with `global_feed_only`. An empty global pending list is not proof that the account has no pending work.

With `course`, the name substring selects enrolled classes. The core fetches each matching class's signed work list, pages results, deduplicates stable IDs and compares the count with the upstream total. Coverage is `matched_course_classes; completeness requires upstream total-count agreement`. `complete: true` means every matched class passed that agreement, not that the global feed or whole account is complete. An incomplete scan stays `complete: false`; missing access signatures are errors, never an assumed zero-work result.

Queries matching more than four classes return `AMBIGUOUS_COURSE`; narrow the name. Scans are bounded to 20 pages per class and a row safety limit, so count disagreement or a bound reached can leave coverage incomplete. This per-course path has been verified; public docs intentionally contain no account-specific counts or course names.

`total_in_feed` counts matching rows in the fetched source after status filtering. `has_more` means more rows in that source, not another upstream page. `limit`/`offset` control output pagination. A response can have `complete: true` and `has_more: true`: the source scan is complete, but the displayed page is not the whole result. Detail `NOT_FOUND` remains scoped to accessible feeds/scans, not all upstream data.

Notices and materials also return `complete: false`. Missing completeness fields on another tool do not establish complete coverage. Preserve timestamps and the upstream date precision; assignment dates may omit the year and must not be silently expanded into a guessed year.

## Errors

`AUTH_REQUIRED` means login failed, was rejected after retry, or needs user verification. `AUTH_COOLDOWN` means a recent failed login is cooling down. `UPSTREAM_SCHEMA` means the parser could not recognize the response, not that the list is empty. `COURSE_ACCESS` means a class did not supply a work-list signature; `AMBIGUOUS_COURSE` asks for a narrower course name. `INVALID_ARGUMENT`, `INVALID_TOOL`, `NOT_FOUND` and `UNSUPPORTED` describe request or capability limits. Treat these as failures, not empty successful results.
