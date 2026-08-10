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

import type { TaskLog } from '../../types'

const domWindow = new Window()
domWindow.location.href = 'https://dashboard.example.com/usage-logs/task'
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { flexRender, getCoreRowModel, useReactTable } =
  await import('@tanstack/react-table')
const { useTaskLogsColumns } = await import('../columns/task-logs-columns')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Click to preview video': 'Click to preview video',
        'Click to view full error message': 'Click to view full error message',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const baseTask: TaskLog = {
  id: 212,
  user_id: 14,
  username: 'user@example.com',
  platform: '1',
  task_id: 'task_video_download',
  action: 'generate',
  channel_id: 9,
  submit_time: 1786304572,
  finish_time: 1786304826,
  progress: '100%',
  status: 'SUCCESS',
  fail_reason: '',
}

function TaskDetailsHarness(props: { log: TaskLog; isAdmin?: boolean }) {
  const columns = useTaskLogsColumns(props.isAdmin ?? false)
  const table = useReactTable({
    data: [props.log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  const cell = table
    .getRowModel()
    .rows[0]?.getAllCells()
    .find((candidate) => candidate.column.id === 'fail_reason')

  if (!cell) return null
  return flexRender(cell.column.columnDef.cell, cell.getContext())
}

type RenderedDetails = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function renderDetails(
  log: TaskLog,
  isAdmin = false
): Promise<RenderedDetails> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <TaskDetailsHarness log={log} isAdmin={isAdmin} />
      </I18nextProvider>
    )
  })

  return { container, root }
}

async function unmountDetails(rendered: RenderedDetails) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

describe('task video download link', () => {
  after(() => domWindow.close())

  test('shows an administrator a secure new-tab link from data.url', async () => {
    const directUrl = 'https://cdn.example.com/generated.mp4?expires=123'
    const rendered = await renderDetails(
      {
        ...baseTask,
        result_url:
          'http://localhost:3000/v1/videos/task_video_download/content',
        data: { url: directUrl },
      },
      true
    )

    const link = rendered.container.querySelector('a')
    assert.ok(link)
    assert.equal(link.textContent, 'Click to preview video')
    assert.equal(link.getAttribute('href'), directUrl)
    assert.equal(link.getAttribute('target'), '_blank')
    assert.equal(link.getAttribute('rel'), 'noopener noreferrer')

    await unmountDetails(rendered)
  })

  test('prefers a direct external result_url over provider response URLs', async () => {
    const rendered = await renderDetails({
      ...baseTask,
      result_url: 'https://canonical.example.com/result.mp4',
      data: { url: 'https://fallback.example.com/result.mp4' },
    })

    assert.equal(
      rendered.container.querySelector('a')?.getAttribute('href'),
      'https://canonical.example.com/result.mp4'
    )
    await unmountDetails(rendered)
  })

  const providerFallbacks: Array<{ name: string; data: unknown; url: string }> = [
    {
      name: 'data.video_url',
      data: { video_url: 'https://cdn.example.com/video-url.mp4' },
      url: 'https://cdn.example.com/video-url.mp4',
    },
    {
      name: 'data.metadata.url',
      data: { metadata: { url: 'https://cdn.example.com/metadata-url.mp4' } },
      url: 'https://cdn.example.com/metadata-url.mp4',
    },
    {
      name: 'data.metadata.origin_video_url',
      data: {
        metadata: {
          origin_video_url: 'https://cdn.example.com/origin-video-url.mp4',
        },
      },
      url: 'https://cdn.example.com/origin-video-url.mp4',
    },
  ]

  for (const scenario of providerFallbacks) {
    test(`uses ${scenario.name} when earlier candidates are absent`, async () => {
      const rendered = await renderDetails({
        ...baseTask,
        data: scenario.data,
      })
      assert.equal(
        rendered.container.querySelector('a')?.getAttribute('href'),
        scenario.url
      )
      await unmountDetails(rendered)
    })
  }

  test('parses a JSON-string task data value', async () => {
    const rendered = await renderDetails({
      ...baseTask,
      data: JSON.stringify({ url: 'https://cdn.example.com/string-data.mp4' }),
    })
    assert.equal(
      rendered.container.querySelector('a')?.getAttribute('href'),
      'https://cdn.example.com/string-data.mp4'
    )
    await unmountDetails(rendered)
  })

  const rejectedProxyUrls = [
    '/v1/videos/task_video_download/content',
    'http://localhost:3000/v1/videos/task_video_download/content',
    'https://api.example.com/v1/videos/task_video_download/content/?token=old',
  ]

  for (const resultUrl of rejectedProxyUrls) {
    test(`does not render authenticated proxy URL ${resultUrl}`, async () => {
      const rendered = await renderDetails({
        ...baseTask,
        result_url: resultUrl,
        data: '{malformed',
      })
      assert.equal(rendered.container.querySelector('a'), null)
      assert.equal(rendered.container.textContent?.trim(), '-')
      await unmountDetails(rendered)
    })
  }

  test('keeps failure details instead of rendering a video link', async () => {
    const rendered = await renderDetails({
      ...baseTask,
      status: 'FAILURE',
      fail_reason: 'Content review failed',
      data: { url: 'https://cdn.example.com/should-not-open.mp4' },
    })
    assert.equal(rendered.container.querySelector('a'), null)
    assert.equal(
      rendered.container.textContent?.includes('Content review failed'),
      true
    )
    await unmountDetails(rendered)
  })

  test('shows a dash for a successful video with no valid direct URL', async () => {
    const rendered = await renderDetails({
      ...baseTask,
      result_url: 'javascript:alert(1)',
      fail_reason: 'legacy non-URL detail',
      data: { url: '../relative-video.mp4' },
    })
    assert.equal(rendered.container.querySelector('a'), null)
    assert.equal(rendered.container.textContent?.trim(), '-')
    await unmountDetails(rendered)
  })
})
