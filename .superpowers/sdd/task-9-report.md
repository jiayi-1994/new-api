# Task 9 report — Final verification and independent review

Branch: `codex/video-pricing-task7`, rebased onto the Task 6 remediation head `fcfa7961`
(clean rebase, both sides' test additions auto-merged). Range verified: `31965da4..HEAD`.

## Gates run

- Formatting: 47 changed Go files gofmt-clean; prettier fixed one import block
  (`cdda888a`); `web/scripts/*.mjs` format:check failures are Windows CRLF checkout
  artifacts on files this branch never touched (index is LF).
- Backend: `go build ./...` ✅ (after frontend build produced `web/dist`);
  `go test ./... -count=1` ✅ except the documented pre-existing Windows flakes in
  `service/channel_affinity_usage_cache_test.go` (reproduced at base with changes stashed).
- Frontend: typecheck ✅; oxlint clean on all 33 branch-changed files; `i18n:sync` clean;
  production build ✅; full `bun test` has a pre-existing full-suite flake in
  `usage-logs/task-video-download.test.tsx` (fails worse at base: 6 vs 4); all
  plan-owned test files pass.
- Diff inspection: no whitespace errors, every task commit present, no unrelated files.

## Independent review gates (six reviewers, whole branch)

- API-contract: PASS (3 Low notes; #3 fixed — channel id removed from user-facing error).
- Testing: PASS (4 Medium). Fixed: dead `buildVideoResolutionOptionUpdate` helper +
  duplicated tests deleted; CAS `expected_documents` wiring assertion added to the
  partial-save test. Accepted risk: no end-to-end test drives the RelayTask retry loop's
  `AllowedChannelTypes` wiring (an attempt was withdrawn at wrap-up per operator
  instruction; `TestTaskRetrySelectionKeepsResolutionChannelsAllowed` +
  `TestChannelCapabilityToggle...` pin the components either side of that one assignment).
- Concurrency: PASS (1 Medium — fixed in `c9b92a06`: plans frozen on an empty model name
  by the distributor are refrozen on the derived model before first billing, closing a
  legacy-hijack bypass on token-pinned submits without a `model` field; regression test).
- Data-integrity: FAIL→fixed (`a3367d2a`): boot/load of the 12 pricing documents was
  all-or-nothing — one legacy-invalid stored document silently reverted ALL pricing to
  compiled defaults. Now: document-level quarantine (lenient lock/load parse), per-document
  degradation to last-published state with loud SysError, repair via `replace_documents`,
  semantic writes rejected 400 without rewriting corrupt raw. Two superseded tests updated
  (load-reject → load-degrade; stored-document 500 → client-actionable 400, consistent with
  the Task 6 entry-level precedent).
- Correctness: FAIL→fixed. High 1 = same as data-integrity High (fixed above). High 2 fixed
  in `6439124e`: drawer pricing completeness gates now run only when the pricing draft was
  actually touched, unblocking metadata-only saves/renames/creates of unpriced models;
  Medium fixed in same commit: tiered_expr models block destructive per-call/per-token
  drawer edits (expression ownership stays with the pricing sheet).
- Frontend TS: FAIL→fixed. High = same drawer gates (fixed). Mediums fixed in `ee0fe223`:
  two locale keys moved inside the `translation` namespace across all 7 locales
  (translations preserved); 400 validation reasons surfaced from `response.data.message`
  in ratio-settings-card and upstream-ratio-sync. Low (`publicationPending ||=`) rebutted:
  sequential commands republish the full document set, last-response-wins is deliberate and
  test-pinned; documented with a code comment.

## Known limitations (carried forward)

- No live MySQL/PostgreSQL verification (static cross-dialect audit only, per reviewer:
  sound). Windows race binaries fail to load (0xc0000139). Serialization aborts on
  MySQL/PG map to 500 not 409 (reviewer Low, untriggerable on SQLite; follow-up).
- Reviewer Lows on ops awareness: orphan-grace vs multi-retry pairing; publication-pending
  in-memory lag until next sync tick; rename retains orphaned target-name entries
  (pre-existing semantics, deliberately pinned).
