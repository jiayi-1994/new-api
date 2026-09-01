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

const { act, cleanup, fireEvent, render, screen } =
  await import('@testing-library/react')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { flexRender, getCoreRowModel, useReactTable } =
  await import('@tanstack/react-table')
const { useTaskLogsColumns } = await import('../columns/task-logs-columns')
const { UsageLogsMobileList } = await import('../usage-logs-mobile-card')

const userEvent = {
  setup() {
    return {
      click: async (element: Element) => {
        await act(async () => {
          fireEvent.click(element)
        })
      },
    }
  },
}

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Click to preview video': 'Click to preview video',
        'Click to preview audio': 'Click to preview audio',
        'Click to view full error message': 'Click to view full error message',
        Model: 'Model',
        Resolution: 'Resolution',
        Result: 'Result',
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

const taskWithVideoFields: TaskLog = {
  ...baseTask,
  id: 5,
  user_id: 1,
  username: 'root',
  task_id: 'task_video_fields',
  action: 'textGenerate',
  channel_id: 1,
  submit_time: 1788098510,
  finish_time: 1788098535,
  progress: '30%',
  status: 'IN_PROGRESS',
  properties: {
    origin_model_name: 'videos-mini',
  },
  billing_details: {
    resolution: '480p',
  },
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

function DesktopTaskFieldsHarness(props: { log: TaskLog }) {
  const columns = useTaskLogsColumns(false)
  const table = useReactTable({
    data: [props.log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  const row = table.getRowModel().rows[0]
  const columnIds = table.getAllLeafColumns().map((column) => column.id)

  return (
    <>
      <output data-testid='column-order'>{columnIds.join(',')}</output>
      {['model', 'resolution'].map((columnId) => {
        const cell = row
          ?.getAllCells()
          .find((candidate) => candidate.column.id === columnId)

        return (
          <div key={columnId} data-testid={`${columnId}-value`}>
            {cell
              ? flexRender(cell.column.columnDef.cell, cell.getContext())
              : 'missing-column'}
          </div>
        )
      })}
    </>
  )
}

function MobileTaskFieldsHarness(props: { log: TaskLog }) {
  const columns = useTaskLogsColumns(false)
  const table = useReactTable({
    data: [props.log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })

  return <UsageLogsMobileList table={table} logCategory='task' />
}

function renderWithI18n(element: React.ReactNode): ReturnType<typeof render> {
  return render(<I18nextProvider i18n={i18n}>{element}</I18nextProvider>)
}

function renderDetails(
  log: TaskLog,
  isAdmin = false
): ReturnType<typeof render> {
  return renderWithI18n(<TaskDetailsHarness log={log} isAdmin={isAdmin} />)
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

  for (const isAdmin of [false, true]) {
    test(`renders a signed relative proxy link for ${isAdmin ? 'administrator' : 'user'} tables`, () => {
      const signedUrl =
        '/v1/videos/task_video_download/content?video_token=signed-capability'
      renderDetails({ ...baseTask, result_url: signedUrl }, isAdmin)

      const link = screen.getByRole('link', { name: 'Click to preview video' })
      assert.equal(link.getAttribute('href'), signedUrl)
      assert.equal(link.getAttribute('target'), '_blank')
      assert.equal(link.getAttribute('rel'), 'noopener noreferrer')
    })
  }

  test('accepts a signed same-origin absolute proxy link', () => {
    const signedUrl =
      'https://dashboard.example.com/v1/videos/task_video_download/content?video_token=signed-capability'
    renderDetails({ ...baseTask, result_url: signedUrl })

    assert.equal(
      screen
        .getByRole('link', { name: 'Click to preview video' })
        .getAttribute('href'),
      signedUrl
    )
  })

  const rejectedLinks: Array<{
    name: string
    result_url?: string
    data?: unknown
    fail_reason?: string
  }> = [
    {
      name: 'an unsigned local link',
      result_url: '/v1/videos/task_video_download/content',
    },
    {
      name: 'a local link with an empty capability',
      result_url: '/v1/videos/task_video_download/content?video_token=%20',
    },
    {
      name: 'a local link with a trailing slash',
      result_url:
        '/v1/videos/task_video_download/content/?video_token=signed-capability',
    },
    {
      name: 'a local link with duplicate capabilities',
      result_url:
        '/v1/videos/task_video_download/content?video_token=one&video_token=two',
    },
    {
      name: 'a dot-normalized proxy path',
      result_url:
        '/v1/videos/task_video_download/./content?video_token=signed-capability',
    },
    {
      name: 'a percent-encoded dot proxy path',
      result_url:
        '/v1/videos/task_video_download/%2e/content?video_token=signed-capability',
    },
    {
      name: 'a link for another task',
      result_url:
        '/v1/videos/another-task/content?video_token=signed-capability',
    },
    {
      name: 'a cross-origin lookalike proxy link',
      result_url:
        'https://dashboard.example.com.evil.test/v1/videos/task_video_download/content?video_token=signed-capability',
    },
    {
      name: 'a direct provider result URL',
      result_url:
        'https://cdn.example.com/generated.mp4?video_token=signed-capability',
    },
    {
      name: 'a malicious protocol URL',
      result_url: 'javascript:window.alert(1)',
    },
    {
      name: 'a data URL',
      result_url: 'data:video/mp4;base64,AAAA',
    },
    {
      name: 'a malformed result URL',
      result_url: 'http://[invalid',
    },
    {
      name: 'a URL-shaped failure reason',
      fail_reason: 'https://cdn.example.com/legacy-video.mp4',
    },
  ]

  for (const scenario of rejectedLinks) {
    test(`renders a dash for ${scenario.name}`, () => {
      renderDetails({ ...baseTask, ...scenario, data: '{malformed' })
      assert.equal(
        screen.queryByRole('link', { name: 'Click to preview video' }),
        null
      )
      assert.ok(screen.getByText('-'))
    })
  }

  const providerFallbacks = [
    {
      name: 'data.url',
      data: {
        url: '/v1/videos/task_video_download/content?video_token=provider',
      },
    },
    {
      name: 'data.video_url',
      data: { video_url: 'https://cdn.example.com/video.mp4' },
    },
    {
      name: 'data.metadata.url',
      data: { metadata: { url: 'https://cdn.example.com/metadata.mp4' } },
    },
    {
      name: 'data.metadata.origin_video_url',
      data: {
        metadata: { origin_video_url: 'https://cdn.example.com/origin.mp4' },
      },
    },
  ]

  for (const scenario of providerFallbacks) {
    test(`does not use ${scenario.name} as a video fallback`, () => {
      renderDetails({ ...baseTask, data: scenario.data })
      assert.equal(screen.queryByRole('link'), null)
      assert.ok(screen.getByText('-'))
    })
  }

  test('does not use provider fallback data when result_url is a JSON string', () => {
    renderDetails({
      ...baseTask,
      data: JSON.stringify({
        url: 'https://cdn.example.com/string-data.mp4',
        video_url: 'https://cdn.example.com/video.mp4',
      }),
    })
    assert.equal(screen.queryByRole('link'), null)
    assert.ok(screen.getByText('-'))
  })

  test('opens failure details when the failure trigger is clicked', async () => {
    renderDetails({
      ...baseTask,
      status: 'FAILURE',
      fail_reason: 'Content review failed',
      data: { url: 'https://cdn.example.com/should-not-open.mp4' },
    })

    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Content review failed' })
    )

    assert.equal(screen.queryByRole('link'), null)
    const dialog = await screen.findByRole('dialog', {
      name: 'Fail Reason Details',
    })
    assert.ok(dialog.textContent?.includes('Content review failed'))
    assert.ok(dialog.textContent?.includes('Error Message'))
  })

  test('shows a dash for a successful video with no valid signed local URL', () => {
    renderDetails({
      ...baseTask,
      result_url: 'javascript:alert(1)',
      fail_reason: 'legacy non-URL detail',
      data: { url: '../relative-video.mp4' },
    })
    assert.equal(screen.queryByRole('link'), null)
    assert.ok(screen.getByText('-'))
  })

  test('opens the Suno audio preview when its trigger is clicked', async () => {
    renderDetails({
      ...baseTask,
      platform: 'suno',
      data: [
        {
          title: 'Generated Suno track',
          audio_url: 'https://cdn.example.com/audio.mp3',
        },
      ],
    })

    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Click to preview audio' })
    )

    const dialog = await screen.findByRole('dialog', { name: 'Audio Preview' })
    assert.ok(dialog.textContent?.includes('Generated Suno track'))
    assert.ok(dialog.querySelector('audio'))
    assert.equal(screen.queryByRole('link'), null)
  })

  test('shows nested model and resolution values after Task ID and before Duration', () => {
    renderWithI18n(<DesktopTaskFieldsHarness log={taskWithVideoFields} />)

    assert.equal(screen.getByTestId('model-value').textContent, 'videos-mini')
    assert.equal(screen.getByTestId('resolution-value').textContent, '480p')

    const columnOrder =
      screen.getByTestId('column-order').textContent?.split(',') ?? []
    assert.deepEqual(
      columnOrder.slice(
        columnOrder.indexOf('task_id'),
        columnOrder.indexOf('duration') + 1
      ),
      ['task_id', 'model', 'resolution', 'duration']
    )
  })

  test('shows a dash when model and resolution are missing', () => {
    const taskWithoutFields: TaskLog = {
      ...taskWithVideoFields,
      properties: undefined,
      billing_details: undefined,
    }

    renderWithI18n(<DesktopTaskFieldsHarness log={taskWithoutFields} />)

    assert.equal(screen.getByTestId('model-value').textContent, '-')
    assert.equal(screen.getByTestId('resolution-value').textContent, '-')
  })

  test('shows model and resolution on the mobile card before Result', () => {
    const rendered = renderWithI18n(
      <MobileTaskFieldsHarness log={taskWithVideoFields} />
    )

    assert.ok(screen.getByText('Model'))
    assert.ok(screen.getByText('videos-mini'))
    assert.ok(screen.getByText('Resolution'))
    assert.ok(screen.getByText('480p'))

    const content = rendered.container.textContent ?? ''
    assert.ok(content.indexOf('Model') < content.indexOf('Result'))
    assert.ok(content.indexOf('Resolution') < content.indexOf('Result'))
  })
})
