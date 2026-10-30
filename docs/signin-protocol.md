# Sign-in protocol and reference comparison

Last updated: 2026-10-30

The comparison uses [ChaoxingSignFaker at aaa62a788bfb503aec95139501e7697e2feb85e2](https://github.com/aquamarine5/ChaoxingSignFaker/tree/aaa62a788bfb503aec95139501e7697e2feb85e2). It covers protocol behavior, not the Android UI, account sharing or every mobile feature. Chaoxing implements these requests independently; no reference source, signing keystore, model asset or embedded service credential is included.

## Discovery and ownership

Class-list `userStatus` is a diagnostic hint. An active sign-in card enters the pre-sign check regardless of that hint. Recent class/group discoveries remain scoped to the enrolled course/class; reusing a recent owned card avoids a second discovery request immediately before signing. Pre-sign and independent attendance reads decide completion.

Course-group history supplements the class feed. A sweep reads at most six groups: groups matching recently polled classes take priority, and remaining capacity rotates through other groups. Partial coverage stays explicit. Group card numeric fields accept either JSON numbers or decimal strings; message timestamps are retained for ordering. Cards without a valid course/class binding are rejected. Results requested for one class exclude cards from other classes. Group discovery still honors the account's discovery cooldown; it does not bypass upstream throttling.

## Request contracts

| Area | Contract | Regression coverage |
| --- | --- | --- |
| Account identity | Participation uses `puid`; Easemob authentication uses its separate `uid`. A missing person ID falls back to the legacy user ID. | `TestReferenceOptionalLocationAndPersonIdentity` |
| Expired pre-sign session | A 403 from the exact mobile pre-sign endpoint permits one login and one retry. An unrelated 403 does not trigger login. A second pre-sign 403 ends the operation. | `TestPreSignForbiddenRecoversSessionOnceAndOnlyThere` |
| QR ownership | The QR activity ID must match the selected activity. Parsing a QR never fetches its URL. A mismatched/invalid ID is rejected before an upstream request. | `TestQRActivityCannotOverrideSelectedActivity` |
| Normal/photo | The shared click/image request includes the empty `useragent` parameter and an object ID only when explicitly provided. No invented photo filename/type envelope is sent. | `TestNormalSignUsesReferenceProtocolAndDeviceIdentity`, `TestPhotoSignSubmitsPanObject` |
| Position | A location sign uses its explicit preset or published center. Code/gesture/QR signs carry optional location and location-result JSON; required location cannot silently become `-1/-1`. | `TestReferenceOptionalLocationAndPersonIdentity`, `TestMissingRequiredLocationNeverSubmits` |
| Coordinate consistency | One jittered sample feeds the query, six-decimal location JSON and submitted-location audit. Published range checks remain enforced. Normal/photo requests do not record an unused preset as sent coordinates. | `TestReferenceOptionalLocationAndPersonIdentity`, `TestLocationSignUsesPresetJitterAndSingleTightRetry` |
| Dynamic captcha | A stored challenge controls the second pass even when static `ifNeedVCode` is zero. Course/class binding and expiry are checked; the expected JSONP callback may end with a semicolon. | `TestSignActivitySolvesDynamicallyRequestedSliderCaptcha`, `TestCaptchaScopeAndJSONPTerminator` |
| Explicit face | Supported normal/location variants keep their base request and use numeric collection flags, `ifCFP=0`, explicit face object/enc and empty `faceCode`/`faceEncAid`. A missing optional status does not discard a returned enc; an explicit rejection still fails. | `TestExplicitFaceKeepsBaseVariantAndWireParameters` |
| Device identity envelope | RSA recovery preserves leading zero bytes and verifies PKCS#1 block type, padding, size and payload before reading the device fields. Tests use a generated key, not upstream private material. | `TestDeviceIdentityRecoveryPreservesLeadingZeroAndChecksPadding` |
| Device refusal | A duplicate-device rejection is terminal. The service reports it without automatically replacing the stable device identity. | `TestCaptchaScopeAndJSONPTerminator` |
| Completion | Submission acceptance and independent attendance confirmation are separate. Pending confirmation resumes read-only and never resubmits merely because a feed ended or disappeared. | `internal/watch/verification_test.go` |

## Deliberate boundaries

- The watcher automatically handles only its configured normal/location and operator-coded password/gesture variants. QR, photo and face inputs remain explicit operations; this comparison does not enable them in unattended mode.
- Required photo cannot be bypassed by choosing the reference app's manual click path. Unsupported photo+face, QR+face and code+face combinations return `UNSUPPORTED` instead of silently dropping one requirement.
- The server uses bounded polling and history reads, not the Android Easemob listener or SDK. A six-group sweep is not complete account coverage.
- School selection stays in account configuration rather than reproducing Android's interactive institution selector. Accounts and challenge state remain isolated.
- Raw tokens, cookies, QR enc, face material and private identity fields are excluded from protocol logs. Logs retain allowlisted fields, request timing, hashes and operation attribution.

## Validation limits

Synthetic tests check request ordering, parameter representations, session retry bounds, class/activity ownership, captcha transitions, cryptographic framing and truthful audit fields. Full tests, race detection, vet and dependency scanning are separate gates. Passing them is not a real classroom sign-in, mobile alert-delivery test or proof of an upstream rate-limit threshold.

The public tool contract is [Tools](tools.md). Watcher configuration and cooldown policy are in [Configuration](configuration.md); production state is maintained by the deployment owner.
