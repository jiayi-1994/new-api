/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export const VIDEO_ERROR_WHITELIST_OPTION_KEY = 'video_error_whitelist.rules'

/** Keyword -> text shown to buyers; empty text shows the sanitized upstream error. */
export type VideoErrorWhitelistRules = Record<string, string>

// A corrupt stored value must still open the editor, so it reads as empty.
export function parseVideoErrorWhitelist(
  value: string | undefined
): VideoErrorWhitelistRules {
  try {
    const parsed: unknown = JSON.parse(value || '{}')
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as VideoErrorWhitelistRules
    }
  } catch {
    /* Falls through to the empty whitelist. */
  }
  return {}
}
