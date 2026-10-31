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
import { describe, expect, it } from 'vitest'

import { parseVideoErrorWhitelist } from '../video-error-whitelist'

describe('parseVideoErrorWhitelist', () => {
  it('reads the stored keyword map', () => {
    expect(parseVideoErrorWhitelist('{"content policy":"blocked"}')).toEqual({
      'content policy': 'blocked',
    })
  })

  it('treats missing and corrupt values as an empty whitelist', () => {
    for (const value of [undefined, '', 'null', '[]', '{']) {
      expect(parseVideoErrorWhitelist(value)).toEqual({})
    }
  })
})
