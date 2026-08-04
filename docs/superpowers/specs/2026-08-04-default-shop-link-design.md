# Default Shop Link Design

## Goal

Change the fallback purchase link to `https://pay.ldxp.cn/shop/FWJQYF6M/klxgc9`.

## Behavior

- When `SHOP_LINK_URL` is unset or empty, initialization uses the new fallback URL.
- A non-empty `SHOP_LINK_URL` continues to override the fallback.
- `SHOP_LINK_ENABLED` continues to control whether the purchase entry is exposed and shown.

## Scope

Only the backend shop-link default and its focused tests change. The frontend, status API contract, translations, deployment files, legacy `TopUpLink`, and existing environment-variable semantics remain unchanged.

## Verification

Add deterministic setting tests for fallback and override behavior, then run the setting package tests and the relevant repository verification. Independently review the final diff for scope and correctness.
