# Video Resolution Pricing Handoff

Date: 2026-08-29
Branch: `v1.4`

## Requirement

Video resolution pricing is per-second only. The price is selected from the
requested provider payload's effective model and resolution. Missing model or
effective resolution pricing returns HTTP 400 before pre-consume or upstream
submission. Legacy `TaskBillingMode` behavior remains unchanged and is ignored
by this new resolution-pricing path.

## Completed

- Tasks 1-4 of the implementation plan are complete and independently approved.
- Configuration, validation, pricing contract, Sora/DashScope integration, and
  provider resolution capability resolvers are committed through `dd7e0a74`.
- Task 5 includes substantial work on frozen billing snapshots, settlement,
  pricing API output, model lifecycle handling, logging, cache invalidation,
  statistics, and regression tests.

## Work in progress

Task 5 is not production-ready. The latest independent review found two open
billing-safety issues:

1. If an upstream request succeeds but local `Task.Insert` fails, the request
   can remain charged without a persisted task for recovery or settlement.
2. Resolution token quota and wallet/subscription pre-consume are not yet
   atomic under concurrent wallet fallback and compensation failure.

A durable `ResolutionBillingReservation` ledger and focused model tests have
been started. Its BillingSession, controller, task attachment, synchronous
insert-failure refund, and orphan-recovery integration still need completion
and independent review.

## Verification at checkpoint

- Package compilation passed:
  `go test ./model ./service ./controller ./relay/helper ./relay/common -run '^$' -count=1`
- Reservation model tests passed:
  `go test ./model -run 'TestResolutionReservation' -count=1`
- `git diff --check` passed.

Full Task 5 focused tests, complete backend tests, frontend tests, build checks,
and final independent review remain pending.

## Resume order

1. Complete the reservation integration using a consistent lock order:
   reservation, funding/token, then task/statistics.
2. Add controller-level failure-injection coverage for upstream success followed
   by task insert failure, including funding, token, log, task, and reservation
   state assertions.
3. Add concurrent wallet/subscription fallback coverage and orphan-sweep tests.
4. Run the Task 5 focused suite and independent code review until no P1/P2
   findings remain.
5. Continue Tasks 6-8 and final verification from the implementation plan.
