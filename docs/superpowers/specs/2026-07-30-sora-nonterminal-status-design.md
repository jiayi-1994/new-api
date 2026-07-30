# Sora Non-Terminal Status Compatibility

## Problem

An upstream OpenAI-compatible video endpoint can return a valid task response
with `status` set to `unknown`, `submitted`, or `created` before its first
polling refresh. The Sora task parser currently leaves these statuses unmapped.
The polling service interprets the resulting empty internal status as an
unrecognized error and permanently fails the local task.

## Design

Normalize the upstream status with `strings.TrimSpace` and `strings.ToLower`
inside `TaskAdaptor.ParseTaskResult`, then extend the existing status switch:

- `unknown` maps to `model.TaskStatusQueued`.
- `submitted` and `created` map to `model.TaskStatusSubmitted`.
- Existing queued, in-progress, success, and failure mappings remain unchanged.

All three added mappings are non-terminal, so the task remains eligible for
later polling. No service-level retry policy or database behavior changes are
included.

## Tests

Add deterministic table-driven tests around the real Sora parser. Cover:

- `unknown`, `submitted`, and `created`.
- Uppercase and surrounding-whitespace normalization.
- Existing representative terminal and in-progress statuses to protect current
  behavior.

Tests use `testify/require` for parsing and `testify/assert` for status values.

## Non-Goals

- Changing how arbitrary missing or unsupported statuses are handled globally.
- Changing polling intervals.
- Changing task billing or refund behavior.
- Changing upstream task creation responses.
