# Infinite Canvas External Menu Design

## Goal

Add a lightweight **Infinite Canvas** entry beside **Playground** in the authenticated sidebar. Selecting it opens `https://canvas.xjy.de5.net/` in a new browser tab.

## Scope

- Add one external sidebar item in the existing Chat group.
- Show or hide it with the existing `chat.playground` module setting.
- Localize the menu label in every supported frontend locale.
- Reuse the sidebar's existing safe external-link behavior.

The change does not add backend APIs, routes, iframe pages, model or billing logic, API-key configuration, or new dependencies.

## Interaction and security

The new item is adjacent to the current Playground item rather than nested beneath it. It uses the existing `external: true` navigation path, which opens a new tab with the project's external-link protections. If the destination is unavailable, the browser displays the destination's normal network error; new-api does not proxy or retry it.

An iframe is intentionally excluded because the destination currently responds with a restrictive `Content-Security-Policy: default-src 'none'; sandbox` header.

## Verification

- Add focused regression coverage for the item label, URL, external marker, and Playground visibility mapping.
- Run the affected frontend tests, i18n synchronization, type checking, lint for changed files, and the production build.
- Review the final diff independently for scope and correctness.
