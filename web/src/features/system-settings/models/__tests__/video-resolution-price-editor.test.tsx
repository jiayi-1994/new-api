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
import assert from 'node:assert/strict'
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { VideoResolutionPriceEditor } =
  await import('../video-resolution-price-editor')
const { validateVideoResolutionPriceRows } =
  await import('../video-resolution-pricing')

type Row = { id: number; resolution: string; price: string }

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Resolution: 'Resolution',
        'USD price per second': 'USD price per second',
        'Add resolution': 'Add resolution',
        'Remove resolution': 'Remove resolution',
        'Each resolution can only be configured once':
          'Each resolution can only be configured once',
        'Use a canonical resolution such as 720p or 4k':
          'Use a canonical resolution such as 720p or 4k',
        'Price must be a finite number greater than zero':
          'Price must be a finite number greater than zero',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function changeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

// 该测试驱动的是真实的受控组件：宿主持有行状态并按同一份校验渲染错误
function EditorHost(props: {
  initialRows: Row[]
  onRows: (rows: Row[]) => void
}) {
  const [rows, setRows] = useState<Row[]>(props.initialRows)
  return (
    <VideoResolutionPriceEditor
      rows={rows}
      errorsByRowId={validateVideoResolutionPriceRows(rows).errorsByRowId}
      onChange={(next) => {
        setRows(next)
        props.onRows(next)
      }}
    />
  )
}

async function renderEditor(initialRows: Row[]) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let latestRows: Row[] = initialRows
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <EditorHost
          initialRows={initialRows}
          onRows={(rows) => {
            latestRows = rows
          }}
        />
      </I18nextProvider>
    )
  })
  return {
    container,
    getRows: () => latestRows,
    cleanup: async () => {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

const resolutionInput = (container: HTMLElement, id: number) =>
  container.querySelector<HTMLInputElement>(`#video-resolution-${id}`)

const rowError = (container: HTMLElement, id: number) =>
  resolutionInput(container, id)
    ?.closest('[data-slot="field"]')
    ?.querySelector('[role="alert"]')?.textContent

describe('video resolution price editor', () => {
  after(() => {
    domWindow.close()
  })

  test('blocks save for duplicate canonical resolutions', async () => {
    const view = await renderEditor([
      { id: 1, resolution: '720P', price: '0.10' },
      { id: 2, resolution: '720p', price: '0.20' },
    ])

    assert.equal(validateVideoResolutionPriceRows(view.getRows()).prices, null)
    assert.match(rowError(view.container, 2) ?? '', /only be configured once/i)
    assert.equal(
      resolutionInput(view.container, 2)?.getAttribute('aria-invalid'),
      'true'
    )

    await view.cleanup()
  })

  test('flags a non-canonical resolution and clears it once corrected', async () => {
    const view = await renderEditor([
      { id: 1, resolution: '1920x1080', price: '0.10' },
    ])

    assert.match(rowError(view.container, 1) ?? '', /canonical resolution/i)

    const input = resolutionInput(view.container, 1)
    assert.ok(input)
    await act(async () => {
      changeInputValue(input, '1080p')
    })

    assert.equal(rowError(view.container, 1), undefined)
    assert.deepEqual(validateVideoResolutionPriceRows(view.getRows()).prices, {
      '1080p': 0.1,
    })

    await view.cleanup()
  })

  test('adds and removes rows through accessible controls', async () => {
    const view = await renderEditor([
      { id: 1, resolution: '720p', price: '0.10' },
    ])

    const addButton = [...view.container.querySelectorAll('button')].find(
      (button) => button.textContent?.includes('Add resolution')
    )
    assert.ok(addButton)
    await act(async () => {
      addButton.click()
    })
    assert.equal(view.getRows().length, 2)

    const removeButton = view.container.querySelector<HTMLButtonElement>(
      'button[aria-label="Remove resolution: 720p"]'
    )
    assert.ok(removeButton)
    await act(async () => {
      removeButton.click()
    })
    assert.deepEqual(
      view.getRows().map((row) => row.id),
      [2]
    )

    await view.cleanup()
  })
})
