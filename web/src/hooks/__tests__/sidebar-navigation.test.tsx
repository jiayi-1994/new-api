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
import { describe, test } from 'node:test'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import type { NavGroup } from '@/components/layout/types'

import { useSidebarConfig } from '../use-sidebar-config'
import { useSidebarData } from '../use-sidebar-data'

async function renderSidebar(playgroundEnabled: boolean): Promise<NavGroup[]> {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: {
      en: { translation: { 'Infinite Canvas': 'Infinite Canvas' } },
    },
  })

  const queryClient = new QueryClient()
  queryClient.setQueryData(['status'], {
    SidebarModulesAdmin: JSON.stringify({
      chat: { enabled: true, playground: playgroundEnabled, chat: true },
    }),
  })

  let navGroups: NavGroup[] = []
  function Probe() {
    const sidebarData = useSidebarData()
    navGroups = useSidebarConfig(sidebarData.navGroups)
    return null
  }

  renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <Probe />
      </I18nextProvider>
    </QueryClientProvider>
  )
  queryClient.clear()
  return navGroups
}

describe('Infinite Canvas sidebar navigation', () => {
  test('renders beside Playground as a safe external link', async () => {
    const navGroups = await renderSidebar(true)
    const chatItems =
      navGroups.find((group) => group.id === 'chat')?.items ?? []
    const canvasItem = chatItems.find(
      (item) => 'url' in item && item.url === 'https://canvas.xjy.de5.net/'
    )

    assert.ok(canvasItem)
    assert.equal(canvasItem.title, 'Infinite Canvas')
    assert.equal(canvasItem.external, true)
    assert.deepEqual(canvasItem.configUrls, ['/playground'])
    assert.equal(
      chatItems.findIndex(
        (item) => 'url' in item && item.url === '/playground'
      ) + 1,
      chatItems.indexOf(canvasItem)
    )
  })

  test('hides when the Playground module is disabled', async () => {
    const navGroups = await renderSidebar(false)
    const chatItems =
      navGroups.find((group) => group.id === 'chat')?.items ?? []

    assert.equal(
      chatItems.some(
        (item) => 'url' in item && item.url === 'https://canvas.xjy.de5.net/'
      ),
      false
    )
  })
})
