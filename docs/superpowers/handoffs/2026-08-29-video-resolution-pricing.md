# Video Resolution Pricing Handoff

Date: 2026-08-29
Branch: `v1.4`

## Requirement

Video resolution pricing is per-second only. The price is selected from the
requested provider payload's effective model and resolution. Missing model or
effective resolution pricing returns HTTP 400 before pre-consume or upstream
submission. Legacy `TaskBillingMode` behavior remains unchanged and is ignored
by this new resolution-pricing path.

## Status

Tasks 1-8 of the implementation plan are complete.

The two open billing-safety issues from the previous checkpoint are closed:

1. **Charged without a persisted task.** Resolution pre-consume now goes through
   the durable `ResolutionBillingReservation` ledger
   (`service.ResolutionReservationFunding`). `Task.Insert` attaches the
   reservation in the same transaction that writes the task and its base
   statistics; `service.PersistSubmittedTask` refunds synchronously and records
   a refund log when the insert fails; `sweepOrphanedResolutionReservations`
   (called from every polling pass) refunds reservations that were never
   attached after a 15-minute grace period.
2. **Non-atomic funding/token pre-consume.** `model.ReserveResolutionBilling`
   commits the ledger row, the wallet/subscription debit and the token debit in
   one transaction, so wallet fallback and compensation failures can no longer
   leave the two halves out of step. The lock order is reservation →
   funding/token → task/statistics everywhere. The interim `*Immediately` quota
   helpers this superseded were removed together with their tests, and the
   invariants they protected moved to
   `model/resolution_billing_reservation_test.go`.

## Verification

Backend: `go build ./...` and `go test ./common ./setting/ratio_setting
./relay/common ./relay/helper ./relay ./relay/channel/task/... ./model ./service
./controller -count=1` — all packages pass.

Frontend (from `web/`): `bun run typecheck`, `bun run lint` (new/changed files
clean), `bun run i18n:sync`, `bun run build`, and
`bun test src/features/system-settings src/features/pricing src/features/models`
— 23 tests pass.

## Known pre-existing issues (not caused by this branch)

- `TestObserveChannelAffinityUsageCacheByRelayFormat_MixedMode` /
  `_UnsupportedModeKeepsEmpty` fail when the whole `./service` package runs but
  pass in isolation. Reproduced unchanged at `dd7e0a74`, and
  `service/channel_affinity*.go` is untouched by this branch.
- `bun run format:check` still reports `web/scripts/{add-copyright,
  format-with-protected-headers,sync-i18n}.mjs`; those files were already
  unformatted before this branch and were deliberately left alone.
- A failed resolution task still refunds through the legacy
  `RefundTaskQuota` path, which adjusts funding and token quota in two steps.
  That behavior is shared with every other task platform and was out of scope
  here.
