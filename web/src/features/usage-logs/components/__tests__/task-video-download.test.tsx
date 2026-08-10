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
import { after, afterEach, describe, test } from 'node:test'

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

const originalGlobalDescriptors = new Map<
  string,
  PropertyDescriptor | undefined
>()

for (const key of domGlobals) {
  originalGlobalDescriptors.set(
    key,
    Object.getOwnPropertyDescriptor(globalThis, key)
  )
  Object.defineProperty(globalThis, key, {
    configurable: true,
    writable: true,
    value: domWindow[key],
  })
}

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
originalGlobalDescriptors.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const { cleanup, render, screen } = await import('@testing-library/react')
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
        'Click to preview audio': 'Click to preview audio',
        'Click to view full error message': 'Click to view full error message',
      },
    },
  },
})

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

function renderDetails(
  log: TaskLog,
  isAdmin = false
): ReturnType<typeof render> {
  return render(
    <I18nextProvider i18n={i18n}>
      <TaskDetailsHarness log={log} isAdmin={isAdmin} />
    </I18nextProvider>
  )
}

describe('task video download link', () => {
  afterEach(() => cleanup())

  after(() => {
    cleanup()
    for (const [key, descriptor] of originalGlobalDescriptors) {
      if (descriptor) {
        Object.defineProperty(globalThis, key, descriptor)
      } else {
        Reflect.deleteProperty(globalThis, key)
      }
    }
    domWindow.close()
  })

  test('shows an administrator a secure new-tab link from data.url', () => {
    const directUrl = 'https://cdn.example.com/generated.mp4?expires=123'
    renderDetails(
      {
        ...baseTask,
        result_url:
          'http://localhost:3000/v1/videos/task_video_download/content',
        data: {
          url: directUrl,
          video_url: 'https://fallback.example.com/video-url.mp4',
          metadata: {
            url: 'https://fallback.example.com/metadata-url.mp4',
            origin_video_url:
              'https://fallback.example.com/origin-video-url.mp4',
          },
        },
      },
      true
    )

    const link = screen.getByRole('link', { name: 'Click to preview video' })
    assert.equal(link.getAttribute('href'), directUrl)
    assert.equal(link.getAttribute('target'), '_blank')
    assert.equal(link.getAttribute('rel'), 'noopener noreferrer')
  })

  test('prefers a direct external result_url over provider response URLs', () => {
    renderDetails({
      ...baseTask,
      result_url: 'https://canonical.example.com/result.mp4',
      data: { url: 'https://fallback.example.com/result.mp4' },
    })

    assert.equal(
      screen
        .getByRole('link', { name: 'Click to preview video' })
        .getAttribute('href'),
      'https://canonical.example.com/result.mp4'
    )
  })

  const providerFallbacks: Array<{ name: string; data: unknown; url: string }> =
    [
      {
        name: 'data.video_url',
        data: {
          url: '/v1/videos/task_video_download/content',
          video_url: 'https://cdn.example.com/video-url.mp4',
          metadata: {
            url: 'https://cdn.example.com/lower-metadata-url.mp4',
            origin_video_url:
              'https://cdn.example.com/lower-origin-video-url.mp4',
          },
        },
        url: 'https://cdn.example.com/video-url.mp4',
      },
      {
        name: 'data.metadata.url',
        data: {
          url: 'http://localhost:3000/v1/videos/task_video_download/content',
          video_url: '/v1/videos/task_video_download/content',
          metadata: {
            url: 'https://cdn.example.com/metadata-url.mp4',
            origin_video_url:
              'https://cdn.example.com/lower-origin-video-url.mp4',
          },
        },
        url: 'https://cdn.example.com/metadata-url.mp4',
      },
      {
        name: 'data.metadata.origin_video_url',
        data: {
          url: '/v1/videos/task_video_download/content',
          video_url: '/v1/videos/task_video_download/content',
          metadata: {
            url: '/v1/videos/task_video_download/content',
            origin_video_url: 'https://cdn.example.com/origin-video-url.mp4',
          },
        },
        url: 'https://cdn.example.com/origin-video-url.mp4',
      },
    ]

  for (const scenario of providerFallbacks) {
    test(`uses ${scenario.name} when earlier candidates are absent or rejected`, () => {
      renderDetails({
        ...baseTask,
        data: scenario.data,
      })
      assert.equal(
        screen
          .getByRole('link', { name: 'Click to preview video' })
          .getAttribute('href'),
        scenario.url
      )
    })
  }

  test('parses a JSON-string task data value', () => {
    renderDetails({
      ...baseTask,
      data: JSON.stringify({ url: 'https://cdn.example.com/string-data.mp4' }),
    })
    assert.equal(
      screen
        .getByRole('link', { name: 'Click to preview video' })
        .getAttribute('href'),
      'https://cdn.example.com/string-data.mp4'
    )
  })

  const rejectedProxyUrls = [
    '/v1/videos/task_video_download/content',
    'http://localhost:3000/v1/videos/task_video_download/content',
    'https://api.example.com/v1/videos/task_video_download/content/?token=old',
  ]

  for (const resultUrl of rejectedProxyUrls) {
    test(`does not render authenticated proxy URL ${resultUrl}`, () => {
      renderDetails({
        ...baseTask,
        result_url: resultUrl,
        data: '{malformed',
      })
      assert.equal(
        screen.queryByRole('link', { name: 'Click to preview video' }),
        null
      )
      assert.ok(screen.getByText('-'))
    })
  }

  test('keeps failure details instead of rendering a video link', () => {
    renderDetails({
      ...baseTask,
      status: 'FAILURE',
      fail_reason: 'Content review failed',
      data: { url: 'https://cdn.example.com/should-not-open.mp4' },
    })
    assert.equal(screen.queryByRole('link'), null)
    assert.ok(screen.getByRole('button', { name: 'Content review failed' }))
  })

  test('shows a dash for a successful video with no valid direct URL', () => {
    renderDetails({
      ...baseTask,
      result_url: 'javascript:alert(1)',
      fail_reason: 'legacy non-URL detail',
      data: { url: '../relative-video.mp4' },
    })
    assert.equal(screen.queryByRole('link'), null)
    assert.ok(screen.getByText('-'))
  })

  test('uses a legacy absolute HTTP(S) fail_reason as the last fallback', () => {
    const legacyUrl = 'https://legacy.example.com/video.mp4'
    renderDetails({
      ...baseTask,
      result_url: '/v1/videos/task_video_download/content',
      fail_reason: legacyUrl,
      data: {
        url: '/v1/videos/task_video_download/content',
        video_url: '/v1/videos/task_video_download/content',
        metadata: {
          url: '/v1/videos/task_video_download/content',
          origin_video_url: '/v1/videos/task_video_download/content',
        },
      },
    })

    assert.equal(
      screen
        .getByRole('link', { name: 'Click to preview video' })
        .getAttribute('href'),
      legacyUrl
    )
  })

  test('preserves the Suno audio preview path', () => {
    renderDetails({
      ...baseTask,
      platform: 'suno',
      data: [{ audio_url: 'https://cdn.example.com/audio.mp3' }],
    })

    assert.ok(screen.getByRole('button', { name: 'Click to preview audio' }))
    assert.equal(screen.queryByRole('link'), null)
  })
})
